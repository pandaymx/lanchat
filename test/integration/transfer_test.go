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

// makeSourceFile 在临时目录生成 size 字节的确定性文件，返回路径、大小与 SHA-256。
func makeSourceFile(t *testing.T, size int64) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建源文件: %v", err)
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var remaining int64 = size
	for remaining > 0 {
		n := len(buf)
		if int64(n) > remaining {
			n = int(remaining)
		}
		for i := 0; i < n; i++ {
			buf[i] = byte((remaining + int64(i)) % 251)
		}
		if _, err := f.Write(buf[:n]); err != nil {
			t.Fatalf("写源文件: %v", err)
		}
		_, _ = h.Write(buf[:n])
		remaining -= int64(n)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭源文件: %v", err)
	}
	return path, hex.EncodeToString(h.Sum(nil))
}

// loopbackCandidate 从发送方实际监听地址构造 127.0.0.1 候选（CandidateAddrs 不含环回）。
func loopbackCandidate(t *testing.T, s *transfer.Sender) string {
	t.Helper()
	_, port, err := net.SplitHostPort(s.Addr().String())
	if err != nil {
		t.Fatalf("解析监听端口: %v", err)
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// TestUnicastSmallFile 验证经 loopback 并发拨号收发 1 MiB+偏移小文件：
// 字节与 SHA-256 一致、.part 已 rename、至少一次进度回调，并验证并发拨号先胜。
func TestUnicastSmallFile(t *testing.T) {
	const size = transfer.ChunkSize + 12345 // 末块非满
	srcPath, sum := makeSourceFile(t, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	sender, err := transfer.NewSender(transfer.SendConfig{
		TransferID: "tf-small",
		File:       src,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "one-time-token",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(sender.Close)

	real := loopbackCandidate(t, sender)
	// 候选中混入立即失败地址与悬挂地址（10.255.255.1 不可路由），验证先胜且不快被拖死。
	candidates := []string{"127.0.0.1:1", "10.255.255.1:4001", real}

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")
	var senderCalls, recvCalls int64
	var senderMu, recvMu sync.Mutex

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- sender.Serve(context.Background(), func(done, speed int64) {
			senderMu.Lock()
			senderCalls++
			senderMu.Unlock()
		})
	}()

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	start := time.Now()
	err = transfer.Receive(ctx, transfer.ReceiveConfig{
		TransferID:  "tf-small",
		Token:       "one-time-token",
		DestPath:    destPath,
		Candidates:  candidates,
		Size:        size,
		SHA256:      sum,
		DialTimeout: 3 * time.Second,
	}, func(done, speed int64) {
		recvMu.Lock()
		recvCalls++
		recvMu.Unlock()
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("Sender.Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("发送方未在 3s 内结束")
	}

	// .part 已 rename，无残留临时文件。
	if _, err := os.Stat(destPath + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part 残留: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("读取落盘文件: %v", err)
	}
	if int64(len(got)) != size {
		t.Fatalf("落盘大小 = %d，期望 %d", len(got), size)
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != sum {
		t.Fatal("落盘 SHA-256 不一致")
	}

	senderMu.Lock()
	sc := senderCalls
	senderMu.Unlock()
	recvMu.Lock()
	rc := recvCalls
	recvMu.Unlock()
	if sc == 0 {
		t.Error("发送方未产生进度回调")
	}
	if rc == 0 {
		t.Error("接收方未产生进度回调")
	}

	// 先胜：真实候选在 3s 拨号超时前获胜，整体拨号耗时应明显短于超时。
	if elapsed >= 3*time.Second {
		t.Errorf("总耗时 %s，期望并发拨号先胜而不等待超时", elapsed)
	}
}

// TestWrongTokenRejected 验证 HELLO 带错误 token 时发送方回 ERROR、
// 接收方失败，且不产生 .part 文件。
func TestWrongTokenRejected(t *testing.T) {
	const size = 4096
	srcPath, sum := makeSourceFile(t, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	sender, err := transfer.NewSender(transfer.SendConfig{
		TransferID: "tf-badtoken",
		File:       src,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "correct-token",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(sender.Close)

	sendErr := make(chan error, 1)
	go func() { sendErr <- sender.Serve(context.Background(), nil) }()

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()
	rerr := transfer.Receive(ctx, transfer.ReceiveConfig{
		TransferID:  "tf-badtoken",
		Token:       "wrong-token",
		DestPath:    destPath,
		Candidates:  []string{loopbackCandidate(t, sender)},
		Size:        size,
		SHA256:      sum,
		DialTimeout: time.Second,
	}, nil)
	if rerr == nil {
		t.Fatal("错误 token 应当被拒绝")
	}
	if !errors.Is(rerr, transfer.ErrPeerError) {
		t.Fatalf("接收错误 = %v，期望 ErrPeerError", rerr)
	}

	select {
	case err := <-sendErr:
		if !errors.Is(err, transfer.ErrBadToken) {
			t.Fatalf("发送错误 = %v，期望 ErrBadToken", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("发送方未及时结束")
	}

	if _, err := os.Stat(destPath + ".part"); !os.IsNotExist(err) {
		t.Fatalf("失败后 .part 应被清理: %v", err)
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatal("错误 token 不应产生落盘文件")
	}
}

// delayedConn 在每次 Read 前等待 delay，用于把 loopback 传输人为拉长到可取消窗口。
type delayedConn struct {
	net.Conn
	delay time.Duration
}

func (c delayedConn) Read(p []byte) (int, error) {
	time.Sleep(c.delay)
	return c.Conn.Read(p)
}

// startThrottleProxy 启动一个对读取限速的 TCP 代理（前端 -> target），
// 返回前端监听地址；关闭代理由 t.Cleanup 处理。
func startThrottleProxy(t *testing.T, target string, delay time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("限速代理监听: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer upstream.Close()

				done := make(chan struct{}, 2)
				// 上行：接收方 -> 发送方（HELLO/ACK/CANCEL），读取限速无必要，直通。
				go func() {
					_, _ = io.Copy(upstream, client)
					done <- struct{}{}
				}()
				// 下行：发送方 -> 接收方，对接收侧读取限速，拉长传输时长。
				go func() {
					_, _ = io.CopyBuffer(client, delayedConn{Conn: upstream, delay: delay}, make([]byte, 4096))
					done <- struct{}{}
				}()
				<-done
			}(c)
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return net.JoinHostPort("127.0.0.1", port)
}

// TestReceiverCancel 验证传输中途取消接收方 ctx：双方及时返回、
// .part 被清理、无最终落盘。
func TestReceiverCancel(t *testing.T) {
	const size = 8 << 20 // 8 MiB
	srcPath, sum := makeSourceFile(t, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	sender, err := transfer.NewSender(transfer.SendConfig{
		TransferID: "tf-cancel",
		File:       src,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "cancel-token",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(sender.Close)

	// 每 4 KiB 停 5ms：8 MiB 约 2000 片 ≈ 10s，确保取消窗口足够。
	proxyAddr := startThrottleProxy(t, loopbackCandidate(t, sender), 5*time.Millisecond)

	sendErr := make(chan error, 1)
	go func() { sendErr <- sender.Serve(context.Background(), nil) }()

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")
	ctx, cancel := context.WithCancel(context.Background())
	recvDone := make(chan error, 1)

	var cancelOnce sync.Once
	go func() {
		recvDone <- transfer.Receive(ctx, transfer.ReceiveConfig{
			TransferID:  "tf-cancel",
			Token:       "cancel-token",
			DestPath:    destPath,
			Candidates:  []string{proxyAddr},
			Size:        size,
			SHA256:      sum,
			DialTimeout: time.Second,
		}, func(done, speed int64) {
			if done > 0 {
				cancelOnce.Do(cancel)
			}
		})
	}()

	select {
	case err := <-recvDone:
		if !errors.Is(err, transfer.ErrCanceled) {
			t.Fatalf("接收错误 = %v，期望 ErrCanceled", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("取消后接收方未在 5s 内返回")
	}

	select {
	case err := <-sendErr:
		if err == nil {
			t.Fatal("发送方在对端取消后应返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("发送方未在对端取消后 3s 内返回")
	}

	if _, err := os.Stat(destPath + ".part"); !os.IsNotExist(err) {
		t.Fatalf("取消后 .part 应被清理: %v", err)
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatal("取消不应产生最终落盘")
	}
}
