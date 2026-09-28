package transfer

import (
	"net"
	"sort"
	"strings"
)

// candidateAddrs 枚举本机「真实局域网」网卡上的 IPv4 地址，拼上数据面
// 监听端口，返回 P2P 候选地址。
//
// 与 discover 的网卡筛选保持一致但在 transfer 内复制精简版，避免 transfer
// 反向依赖 discover。排除：loopback、虚拟/隧道网卡（docker/veth/tun/wg/
// tailscale 等）、CGNAT 100.64/10；仅保留 RFC1918 私网 v4。
//
// 候选按 IP 排序，使调用方可配合错峰拨号（Happy Eyeballs）；网卡同 /24
// 优先的排序在接收方拨号侧完成（需要对端参考地址），这里仅保证稳定顺序。
func candidateAddrs(port string) []string {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags & net.FlagUp) == 0 {
			continue
		}
		if isVirtualIfaceName(ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || isCGNATIP(v4) || !v4.IsPrivate() {
				continue
			}
			ips = append(ips, v4)
		}
	}

	sort.Slice(ips, func(i, j int) bool { return ips[i].String() < ips[j].String() })

	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out
}

// isVirtualIfaceName 按网卡名前缀/关键字识别虚拟、隧道与回环网卡。
func isVirtualIfaceName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range []string{
		"lo", "docker", "veth", "br-", "virbr", "tun", "tap",
		"wg", "tailscale", "zt", "utun", "cni", "flannel",
	} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// isCGNATIP 判断是否为 RFC6598 运营商级 NAT 网段 100.64.0.0/10。
func isCGNATIP(ip net.IP) bool {
	return ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

// parseCandidateIP 从 host:port 候选里解析 IPv4。
func parseCandidateIP(candidate string) net.IP {
	host, _, err := net.SplitHostPort(candidate)
	if err != nil {
		host = candidate
	}
	return net.ParseIP(host).To4()
}

// sameSubnet 判断两个 v4 地址是否处于同一 /24。
func sameSubnet(a, b net.IP) bool {
	if a == nil || b == nil {
		return false
	}
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2]
}

// preferLocalSubnet 把与本机任一私网网卡 IP 同 /24 的候选排到最前（稳定），
// 用于多网卡环境优先选直连同网段的对端地址。
func preferLocalSubnet(candidates []string) []string {
	local := localPrivateIPv4()
	sort.SliceStable(candidates, func(i, j int) bool {
		ci := parseCandidateIP(candidates[i])
		cj := parseCandidateIP(candidates[j])
		return sharesSubnetWith(ci, local) && !sharesSubnetWith(cj, local)
	})
	return candidates
}

// sharesSubnetWith 判断 ip 是否与 refs 中任一地址同 /24。
func sharesSubnetWith(ip net.IP, refs []net.IP) bool {
	for _, r := range refs {
		if sameSubnet(ip, r) {
			return true
		}
	}
	return false
}

// localPrivateIPv4 枚举本机私网 IPv4（不含 loopback/CGNAT）。
func localPrivateIPv4() []net.IP {
	var out []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags&net.FlagUp) == 0 || isVirtualIfaceName(ifi.Name) {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || isCGNATIP(v4) || !v4.IsPrivate() {
				continue
			}
			out = append(out, v4)
		}
	}
	return out
}
