package discover

import (
	"net"
	"strings"
)

// lanInterfaces 返回应在其上做 mDNS 的「真实局域网」网卡名。
//
// 必须排除虚拟/隧道网卡：dnssd 的 responder 会在 *每张* 多播网卡上反复
// probe，若把 Tailscale（eth3）与物理 LAN（eth4）一起纳入，多网卡 probe
// 经多播环回互相灌入 responder 的单消费主循环，形成消息洪泛，占满循环后
// 迟到的 PTR 查询得不到应答。实测仅在物理 LAN 网卡上注册时浏览稳定。
func lanInterfaces() []string {
	var names []string
	for _, ifi := range multicastIfaces() {
		if isVirtualIface(ifi.Name) {
			continue
		}
		if !ifaceHasPrivateIPv4(ifi) {
			continue
		}
		names = append(names, ifi.Name)
	}
	return names
}

// multicastIfaces 返回当前活动且支持多播、并至少有一个地址的网卡。
func multicastIfaces() []net.Interface {
	var out []net.Interface
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags&net.FlagUp) == 0 || (ifi.Flags&net.FlagMulticast) == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// isVirtualIface 按网卡名前缀/关键字识别虚拟、隧道与回环网卡。
func isVirtualIface(name string) bool {
	lower := strings.ToLower(name)
	prefixes := []string{
		"lo", "docker", "veth", "br-", "virbr", "tun", "tap",
		"wg", "tailscale", "zt", "utun", "cni", "flannel",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// ifaceHasPrivateIPv4 判断网卡是否持有 RFC1918 私网 IPv4 地址。
// Tailscale 的 100.64.0.0/10（CGNAT）据此被排除，loopback 也不在此列。
func ifaceHasPrivateIPv4(ifi net.Interface) bool {
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		if isCGNAT(v4) { // Tailscale 100.64/10 不算局域网
			continue
		}
		if v4.IsPrivate() && !v4.IsLoopback() {
			return true
		}
	}
	return false
}

// cgnatNet 是 RFC6598 运营商级 NAT 网段 100.64.0.0/10，Tailscale 默认占用。
var cgnatNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

func isCGNAT(ip net.IP) bool {
	return cgnatNet.Contains(ip)
}
