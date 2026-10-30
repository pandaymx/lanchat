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

func TestMultipathOutOfOrderChunkBitmapResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "resume-src.bin")
	dstPath := filepath.Join(dir, "resume-dst.bin")

	raw := make([]byte, 6*ChunkSize)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	shaHex := hex.EncodeToString(sum[:])

	// 模拟前一次多路径并发传输中断：乱序完成了 Chunk {0, 2, 4, 5}，仅缺失 Chunk {1, 3}
	partPath := dstPath + ".part"
	metaPath := dstPath + ".meta"
	partFile, err := os.OpenFile(partPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	meta := &metaState{
		TransferID: "mp-tx-resume",
		Name:       "resume-dst.bin",
		Size:       int64(len(raw)),
		SHA256:     shaHex,
		ChunkSize:  ChunkSize,
	}
	for _, doneIdx := range []int{0, 2, 4, 5} {
		off := int64(doneIdx) * int64(ChunkSize)
		if _, err := partFile.WriteAt(raw[off:off+int64(ChunkSize)], off); err != nil {
			t.Fatal(err)
		}
		meta.markChunkDone(doneIdx, 6, int64(len(raw)))
	}
	_ = partFile.Close()
	if err := saveMeta(metaPath, meta); err != nil {
		t.Fatal(err)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer srcFile.Close()

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

	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- ServeMultipathListeners(ctx, SendConfig{
			TransferID: "mp-tx-resume",
			File:       srcFile,
			Name:       "resume-src.bin",
			Size:       int64(len(raw)),
			SHA256:     shaHex,
			Token:      "mp-resume-tok",
		}, []net.Listener{ln1, ln2}, nil)
	}()

	stats, err := ReceiveMultipath(ctx, ReceiveConfig{
		TransferID:  "mp-tx-resume",
		Token:       "mp-resume-tok",
		DestPath:    dstPath,
		Candidates:  []string{ln1.Addr().String(), ln2.Addr().String()},
		Size:        int64(len(raw)),
		SHA256:      shaHex,
		DialTimeout: 500 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("ReceiveMultipath resume failed: %v", err)
	}
	if err := <-sendErrCh; err != nil {
		t.Fatalf("ServeMultipathListeners resume failed: %v", err)
	}

	pulledChunks := 0
	for _, r := range stats.Routes {
		pulledChunks += r.ChunksDone
	}
	if pulledChunks != 2 {
		t.Fatalf("expected only 2 missing chunks pulled on resume, got %d (stats=%+v)", pulledChunks, stats)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("destination content mismatch after out-of-order bitmap resume")
	}
}

func TestTieredChunkBufferPoolReuse(t *testing.T) {
	for _, sz := range []int{1024, 128 * 1024, ChunkSize, ChunkSize + dataHeaderSize} {
		b := AcquireChunkBuffer(sz)
		if len(b) != sz {
			t.Fatalf("AcquireChunkBuffer(%d) len = %d", sz, len(b))
		}
		ReleaseChunkBuffer(b)
	}
}
