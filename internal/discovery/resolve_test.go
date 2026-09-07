package discovery

import (
	"net"
	"strconv"
	"testing"
)

// TestProbeLocalHub 验证回环探测：起一个 TCP 服务在测试端口，探测应命中。
func TestProbeLocalHub(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	orig := LocalFallbackPorts
	LocalFallbackPorts = []int{port}
	t.Cleanup(func() { LocalFallbackPorts = orig })

	u := probeLocalHub()
	want := "ws://127.0.0.1:" + strconv.Itoa(port) + DefaultWSPath
	if u != want {
		t.Errorf("probeLocalHub = %q, want %q", u, want)
	}
}

// TestProbeLocalHubNoHit 无监听时探测返回空。
func TestProbeLocalHubNoHit(t *testing.T) {
	orig := LocalFallbackPorts
	LocalFallbackPorts = []int{1} // 端口 1 不可能监听（需要特权）
	t.Cleanup(func() { LocalFallbackPorts = orig })

	if u := probeLocalHub(); u != "" {
		t.Errorf("probeLocalHub = %q, want empty", u)
	}
}
