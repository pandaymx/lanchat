package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMultipathConcurrentChunkScheduling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "multipath-src.bin")
	dstPath := filepath.Join(dir, "multipath-dst.bin")

	// 6 MiB (6 个 1 MiB Chunk)
	raw := make([]byte, 6*ChunkSize)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	shaHex := hex.EncodeToString(sum[:])

	srcFile, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer srcFile.Close()

	// 模拟有线网卡、Wi-Fi 网卡与 Tailscale 隧道 3 条并行监听路径
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln1.Close()
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()
	ln3, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln3.Close()

	ranked := ProbeAndRankCandidates(ctx, []string{
		ln1.Addr().String(),
		ln2.Addr().String(),
		ln3.Addr().String(),
		"127.0.0.1:1",
	}, 300*time.Millisecond)
	if len(ranked) != 3 {
		t.Fatalf("ProbeAndRankCandidates len = %d, want 3", len(ranked))
	}

	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- ServeMultipathListeners(ctx, SendConfig{
			TransferID: "mp-tx-1",
			File:       srcFile,
			Name:       "multipath-src.bin",
			Size:       int64(len(raw)),
			SHA256:     shaHex,
			Token:      "mp-token-secret",
		}, []net.Listener{ln1, ln2, ln3}, func(done, speed int64) {})
	}()

	stats, err := ReceiveMultipath(ctx, ReceiveConfig{
		TransferID: "mp-tx-1",
		Token:      "mp-token-secret",
		DestPath:   dstPath,
		Candidates: []string{
			"127.0.0.1:1", // 故障链路（自动切换）
			ln1.Addr().String(),
			ln2.Addr().String(),
			ln3.Addr().String(),
		},
		Size:        int64(len(raw)),
		SHA256:      shaHex,
		DialTimeout: 500 * time.Millisecond,
	}, func(done, speed int64) {})
	if err != nil {
		t.Fatalf("ReceiveMultipath failed: %v", err)
	}
	if err := <-sendErrCh; err != nil {
		t.Fatalf("ServeMultipathListeners failed: %v", err)
	}

	if stats.TotalChunks != 6 || stats.PathsUsed < 1 || stats.FailoverCount < 1 {
		t.Fatalf("unexpected MultipathStats: %+v", stats)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("destination file content mismatch after multipath transfer")
	}
}

func TestMultipathMidTransferLinkDropFailover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "failover-src.bin")
	dstPath := filepath.Join(dir, "failover-dst.bin")

	raw := make([]byte, 3*ChunkSize+12345)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	shaHex := hex.EncodeToString(sum[:])

	srcFile, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer srcFile.Close()

	// 链路 A：连接建立后立即闪断（模拟 Wi-Fi 拔掉/断流）
	flakyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer flakyLn.Close()
	go func() {
		for {
			c, err := flakyLn.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	// 链路 B：稳定有线/Tailscale 路径
	sender, err := NewSender(SendConfig{
		TransferID: "mp-tx-2",
		File:       srcFile,
		Name:       "failover-src.bin",
		Size:       int64(len(raw)),
		SHA256:     shaHex,
		Token:      "mp-token-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- sender.ServeMultipath(ctx, nil)
	}()

	stats, err := ReceiveMultipath(ctx, ReceiveConfig{
		TransferID: "mp-tx-2",
		Token:      "mp-token-2",
		DestPath:   dstPath,
		Candidates: []string{
			flakyLn.Addr().String(),
			sender.Addr().String(),
		},
		Size:        int64(len(raw)),
		SHA256:      shaHex,
		DialTimeout: 500 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("ReceiveMultipath failover failed: %v", err)
	}
	if err := <-sendErrCh; err != nil {
		t.Fatalf("sender.ServeMultipath failed: %v", err)
	}
	if stats.FailoverCount < 1 || stats.TotalChunks != 4 {
		t.Fatalf("expected failover >= 1 and 4 chunks, got %+v", stats)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("destination content mismatch after link drop failover")
	}
}
