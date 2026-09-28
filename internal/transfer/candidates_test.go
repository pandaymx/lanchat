package transfer

import (
	"net"
	"testing"
)

func TestIsVirtualIfaceName(t *testing.T) {
	virtual := []string{"lo", "docker0", "veth123", "br-ab", "virbr0", "tun0", "tap3", "wg0", "tailscale0", "zt0", "utun1"}
	for _, n := range virtual {
		if !isVirtualIfaceName(n) {
			t.Errorf("%s 应判定为虚拟网卡", n)
		}
	}
	real := []string{"eth0", "enp3s0", "wlan0", "ens5"}
	for _, n := range real {
		if isVirtualIfaceName(n) {
			t.Errorf("%s 不应判定为虚拟网卡", n)
		}
	}
}

func TestIsCGNATIP(t *testing.T) {
	cases := map[string]bool{
		"100.63.255.255":  false,
		"100.64.0.0":      true,
		"100.100.100.100": true,
		"100.127.255.255": true,
		"100.128.0.0":     false,
		"192.168.1.1":     false,
	}
	for ip, want := range cases {
		if got := isCGNATIP(net.ParseIP(ip).To4()); got != want {
			t.Errorf("isCGNATIP(%s) = %v want %v", ip, got, want)
		}
	}
}

func TestSameSubnet(t *testing.T) {
	a := net.ParseIP("192.168.1.10").To4()
	b := net.ParseIP("192.168.1.99").To4()
	c := net.ParseIP("192.168.2.10").To4()
	if !sameSubnet(a, b) {
		t.Error("同 /24 应返回 true")
	}
	if sameSubnet(a, c) {
		t.Error("跨 /24 应返回 false")
	}
	if sameSubnet(a, nil) {
		t.Error("nil 应返回 false")
	}
}

func TestPreferLocalSubnet(t *testing.T) {
	// 直接验证排序核心：构造候选与本地网段匹配场景较难（依赖主机网卡），
	// 这里用 parseCandidateIP 验证解析健壮性。
	if got := parseCandidateIP("10.0.0.5:42100"); got.String() != "10.0.0.5" {
		t.Fatalf("parseCandidateIP = %v", got)
	}
	if got := parseCandidateIP("10.0.0.5"); got.String() != "10.0.0.5" {
		t.Fatalf("无端口 parseCandidateIP = %v", got)
	}
}
