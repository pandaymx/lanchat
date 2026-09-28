package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/transfer"
)

// TestResumeAfterDrop 模拟崩溃/断网（不发 CANCEL，直接断连）：
// 第一次只传部分字节后中断，.part/.meta 保留；用相同 TransferID 再次接收，
// 从断点续传，最终落盘 SHA-256 正确，且无临时文件残留。
func TestResumeAfterDrop(t *testing.T) {
	const size = 8 << 20 // 8 MiB
	const transferID = "tf-resume"
	srcPath, sum := makeSourceFile(t, size)

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")

	// 第一次：限速传输，收到部分字节后直接 cancel 接收 goroutine（模拟进程退出，
	// 不发 CANCEL）。这里使用独立可 cancel 的 ctx，且中断方式为“连接被切断”。
	partial := runPartialReceive(t, srcPath, sum, size, transferID, destPath)
	if partial <= 0 || partial >= size {
		t.Fatalf("首次应只收到部分字节，got %d", partial)
	}

	// 临时文件应保留。
	if _, err := os.Stat(destPath + ".part"); err != nil {
		t.Fatalf("崩溃后 .part 应保留: %v", err)
	}
	if _, err := os.Stat(destPath + ".meta"); err != nil {
		t.Fatalf("崩溃后 .meta 应保留: %v", err)
	}

	// 第二次：新建发送方（新端口），相同 TransferID，从断点续传至完成。
	src2, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("重新打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src2.Close() })

	sender2, err := transfer.NewSender(transfer.SendConfig{
		TransferID: transferID,
		File:       src2,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "resume-token-2",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(sender2.Close)

	sendErr := make(chan error, 1)
	go func() { sendErr <- sender2.Serve(context.Background(), nil) }()

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	var resumedFrom int64 = -1
	err = transfer.Receive(ctx, transfer.ReceiveConfig{
		TransferID:  transferID,
		Token:       "resume-token-2",
		DestPath:    destPath,
		Candidates:  []string{loopbackCandidate(t, sender2)},
		Size:        size,
		SHA256:      sum,
		DialTimeout: time.Second,
	}, func(done, _ int64) {
		if resumedFrom < 0 {
			resumedFrom = done
		}
	})
	if err != nil {
		t.Fatalf("续传 Receive: %v", err)
	}

	// 首个进度点应从上次断点附近开始，而不是 0（证明没有重传全部）。
	if resumedFrom < partial {
		t.Errorf("续传起点 = %d，应 >= 首次已收 %d", resumedFrom, partial)
	}

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("Sender.Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("发送方未在 3s 内结束")
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("读取落盘: %v", err)
	}
	if int64(len(got)) != size {
		t.Fatalf("落盘大小 = %d want %d", len(got), size)
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != sum {
		t.Fatal("续传后落盘 SHA-256 不一致")
	}

	for _, suffix := range []string{".part", ".meta"} {
		if _, err := os.Stat(destPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("完成后 %s 不应残留", suffix)
		}
	}
}

// runPartialReceive 启动一次限速接收，收到至少 1 MiB 后在代理层强行切断连接
// （模拟 kill -9 / 断网：无 CANCEL 帧、接收 ctx 未取消），返回已落盘字节数。
func runPartialReceive(t *testing.T, srcPath, sum string, size int64, transferID, destPath string) int64 {
	t.Helper()

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	sender, err := transfer.NewSender(transfer.SendConfig{
		TransferID: transferID,
		File:       src,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "resume-token-1",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}

	proxyAddr, drop := startDropProxy(t, loopbackCandidate(t, sender), 5*time.Millisecond)

	sendErr := make(chan error, 1)
	go func() { sendErr <- sender.Serve(context.Background(), nil) }()

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	recvDone := make(chan error, 1)
	var dropOnce sync.Once

	var gotDone int64
	go func() {
		recvDone <- transfer.Receive(ctx, transfer.ReceiveConfig{
			TransferID:  transferID,
			Token:       "resume-token-1",
			DestPath:    destPath,
			Candidates:  []string{proxyAddr},
			Size:        size,
			SHA256:      sum,
			DialTimeout: time.Second,
		}, func(done, _ int64) {
			gotDone = done
			if done >= transfer.MinCreditBytes {
				dropOnce.Do(drop) // 模拟 kill -9：代理层硬切断，无 CANCEL
			}
		})
	}()

	select {
	case err := <-recvDone:
		// 连接被硬切断：返回的应是普通连接错误，而非 ErrCanceled（否则会误删临时文件）。
		if errors.Is(err, transfer.ErrCanceled) {
			t.Fatalf("硬切断不应得到 ErrCanceled（否则临时文件会被删）: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("首次接收未在预期时间内中断")
	}

	sender.Close()
	_ = src.Close()
	<-sendErr // 发送方因断连返回，忽略其错误
	return gotDone
}

// startDropProxy 与 startThrottleProxy 同构：上行（接收方→发送方）直通，
// 下行（发送方→接收方）按 delay 限速以拉长传输窗口；额外跟踪连接并返回 drop，
// 调用后立即硬切断上下游，模拟网络中断 / 进程被 kill -9（无 CANCEL 帧）。
func startDropProxy(t *testing.T, target string, delay time.Duration) (addr string, drop func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("drop proxy 监听: %v", err)
	}

	var mu sync.Mutex
	var conns []net.Conn
	killed := false
	register := func(c net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		if killed {
			_ = c.Close()
			return
		}
		conns = append(conns, c)
	}
	drop = func() {
		mu.Lock()
		defer mu.Unlock()
		killed = true
		for _, c := range conns {
			_ = c.Close()
		}
		conns = nil
	}

	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					_ = client.Close()
					return
				}
				register(client)
				register(upstream)

				done := make(chan struct{}, 2)
				// 上行：接收方 -> 发送方（HELLO/ACK），直通。
				go func() {
					_, _ = io.Copy(upstream, client)
					done <- struct{}{}
				}()
				// 下行：发送方 -> 接收方，限速拉长时长。
				go func() {
					_, _ = io.CopyBuffer(client, delayedConn{Conn: upstream, delay: delay}, make([]byte, 4096))
					done <- struct{}{}
				}()
				<-done
				_ = client.Close()
				_ = upstream.Close()
			}(client)
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		drop()
	})

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return net.JoinHostPort("127.0.0.1", port), drop
}
