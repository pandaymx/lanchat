package discover

import (
	"net"
	"testing"
)

func TestIsVirtualIface(t *testing.T) {
	virtual := []string{"lo", "docker0", "veth123", "br-abc", "virbr0", "tun0", "tap0", "wg0", "tailscale0", "zt0", "utun3"}
	for _, name := range virtual {
		if !isVirtualIface(name) {
			t.Errorf("网卡 %q 应被识别为虚拟网卡", name)
		}
	}
	physical := []string{"eth0", "eth4", "enp3s0", "wlan0", "ens192"}
	for _, name := range physical {
		if isVirtualIface(name) {
			t.Errorf("网卡 %q 不应被识别为虚拟网卡", name)
		}
	}
}

func TestIsCGNAT(t *testing.T) {
	cases := map[string]bool{
		"100.64.0.1":      true,
		"100.127.255.255": true,
		"100.88.89.39":    true, // Tailscale 地址
		"100.63.0.1":      false,
		"100.128.0.1":     false,
		"192.168.1.47":    false,
		"10.0.0.1":        false,
	}
	for ip, want := range cases {
		if got := isCGNAT(net.ParseIP(ip)); got != want {
			t.Errorf("isCGNAT(%s) = %v, want %v", ip, got, want)
		}
	}
}

// TestLanInterfaceNames 校验导出给服务端启动日志的排障视图与内部选择逻辑同源：
// 不泄漏虚拟/隧道网卡，且给出的名字都能在本机解析。
func TestLanInterfaceNames(t *testing.T) {
	for _, name := range LanInterfaceNames() {
		if isVirtualIface(name) {
			t.Errorf("LanInterfaceNames 不应返回虚拟网卡 %q", name)
		}
		if _, err := net.InterfaceByName(name); err != nil {
			t.Errorf("LanInterfaceNames 返回了不存在的网卡 %q: %v", name, err)
		}
	}
}
