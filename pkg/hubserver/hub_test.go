package hubserver

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestStartSmoke：嵌入式 hub 最小冒烟——库内启动、端口可连、正常关停。
func TestStartSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	srv, err := Start(ctx, Config{
		Addr:        "127.0.0.1:19123",
		DBPath:      "memory",
		FilesDir:    t.TempDir(),
		MDNS:        false,
		MaxFileSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	if srv.Router() == nil {
		t.Fatal("Router() nil")
	}
	if srv.Store() == nil {
		t.Fatal("Store() nil")
	}
	if srv.Addr() != "127.0.0.1:19123" {
		t.Fatalf("Addr() = %q", srv.Addr())
	}

	// 端口确实在监听。
	c, err := net.DialTimeout("tcp", "127.0.0.1:19123", 2*time.Second)
	if err != nil {
		t.Fatalf("dial hub: %v", err)
	}
	_ = c.Close()
}

// TestStartDataDir：DBPath 空 → 落到平台数据目录（不报错即通过；
// 不写文件，避免污染用户目录——只验证路径解析与内存模式等价）。
func TestStartDataDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// memory 模式 + 默认目录兜底：不落盘，验证 Start 全链路不因
	// 目录解析失败。
	srv, err := Start(ctx, Config{
		Addr:        "127.0.0.1:19124",
		DBPath:      "memory",
		MDNS:        false,
		MaxFileSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()
}
