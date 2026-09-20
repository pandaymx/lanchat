package mobile

import (
	"testing"
)

// TestStartStop：嵌入式 hub 起停生命周期——Start 返回可连 ws 地址、
// 幂等重复 Start、Stop 后 Addr 清空、再 Start 可用。
func TestStartStop(t *testing.T) {
	dir := t.TempDir()

	ws, err := Start(dir, "test-node", "test", false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if ws == "" {
		t.Fatal("Start returned empty ws addr")
	}
	if Addr() != ws {
		t.Fatalf("Addr() = %q, want %q", Addr(), ws)
	}

	// 幂等：重复 Start 返回同一地址且不报错。
	ws2, err := Start(dir, "test-node", "test", false)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if ws2 != ws {
		t.Fatalf("second Start addr = %q, want %q", ws2, ws)
	}

	Stop()
	if Addr() != "" {
		t.Fatalf("Addr after Stop = %q, want empty", Addr())
	}

	// 停止后可再起。
	ws3, err := Start(dir, "test-node", "test", false)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if ws3 == "" {
		t.Fatal("restart returned empty ws addr")
	}
	Stop()
}
