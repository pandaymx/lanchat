package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/transfer"
)

// TestReverseDial 验证反向拨号：发送方不可被拨入（FILE_OFFER.Candidates 为空），
// 接收方监听 P2P 端口并回传候选，发送方反向拨入完成发送。
// 数据面仍是拨号方（发送方）发 HELLO，落盘 SHA-256 正确。
func TestReverseDial(t *testing.T) {
	const size = 4 << 20 // 4 MiB
	const transferID = "tf-reverse"
	srcPath, sum := makeSourceFile(t, size)

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")

	// 1. 接收方先监听（对应收到空 candidates 的 FILE_OFFER 后启动）。
	receiver, err := transfer.NewReceiver(transfer.ReceiveConfig{
		TransferID: transferID,
		Token:      "reverse-token",
		DestPath:   destPath,
		Size:       size,
		SHA256:     sum,
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	t.Cleanup(receiver.Close)

	recvErr := make(chan error, 1)
	go func() { recvErr <- receiver.Serve(context.Background(), nil) }()

	// 2. 模拟接收方回 FILE_REVERSE，发送方拿到候选后反向拨号发送。
	candidates := []string{loopbackAddr(receiver.Addr().String())}

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- transfer.DialSend(ctx, transfer.SendConfig{
			TransferID: transferID,
			File:       src,
			Name:       "source.bin",
			Size:       size,
			SHA256:     sum,
			Token:      "reverse-token",
		}, candidates, time.Second, nil)
	}()

	select {
	case err := <-sendErrCh:
		if err != nil {
			t.Fatalf("DialSend: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("发送方未在 10s 内完成")
	}

	select {
	case err := <-recvErr:
		if err != nil {
			t.Fatalf("Receiver.Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("接收方未在 3s 内结束")
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
		t.Fatal("反向拨号落盘 SHA-256 不一致")
	}
}

// loopbackAddr 把候选地址的 host 固定为 127.0.0.1，便于在 loopback 下拨入。
func loopbackAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}
