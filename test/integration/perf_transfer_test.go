//go:build perf

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/transfer"
)

// makeLargeSourceFile 流式生成 size 字节确定性文件，避免大文件内容常驻内存。
func makeLargeSourceFile(t *testing.T, size int64) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source-large.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建大源文件: %v", err)
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
			t.Fatalf("写大源文件: %v", err)
		}
		_, _ = h.Write(buf[:n])
		remaining -= int64(n)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭大源文件: %v", err)
	}
	return path, hex.EncodeToString(h.Sum(nil))
}

// TestPerf_1GiB_Throughput 验证 1 GiB loopback 传输吞吐 ≥110 MB/s 且堆增长 <20 MiB。
// 真实千兆线速以双机环境人工结果为准；此处主要保证零内存膨胀。
func TestPerf_1GiB_Throughput(t *testing.T) {
	const size int64 = 1 << 30 // 1 GiB
	srcPath, sum := makeLargeSourceFile(t, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	sender, err := transfer.NewSender(transfer.SendConfig{
		TransferID: "tf-perf",
		File:       src,
		Name:       "source-large.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "perf-token",
		ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(sender.Close)

	_, port, err := net.SplitHostPort(sender.Addr().String())
	if err != nil {
		t.Fatalf("解析监听端口: %v", err)
	}
	candidate := net.JoinHostPort("127.0.0.1", port)
	destPath := filepath.Join(t.TempDir(), "recv", "source-large.bin")

	sendErr := make(chan error, 1)
	go func() { sendErr <- sender.Serve(context.Background(), nil) }()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	rcvErr := transfer.Receive(ctx, transfer.ReceiveConfig{
		TransferID:  "tf-perf",
		Token:       "perf-token",
		DestPath:    destPath,
		Candidates:  []string{candidate},
		Size:        size,
		SHA256:      sum,
		DialTimeout: 3 * time.Second,
	}, nil)
	elapsed := time.Since(start)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("Sender.Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("发送方未结束")
	}
	if rcvErr != nil {
		t.Fatalf("Receive: %v", rcvErr)
	}

	seconds := elapsed.Seconds()
	mbps := float64(size) / (1024 * 1024) / seconds
	heapGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)

	t.Logf("1 GiB 耗时 %s，吞吐 %.2f MiB/s（%.2f MB/s），堆增长 %d MiB",
		elapsed, mbps, float64(size)/1e6/seconds, heapGrowth/(1<<20))

	if mbps < 110 {
		t.Errorf("吞吐 %.2f MiB/s，期望 ≥110 MiB/s", mbps)
	}
	if heapGrowth >= 20<<20 {
		t.Errorf("堆增长 %d MiB，期望 <20 MiB", heapGrowth/(1<<20))
	}
}
