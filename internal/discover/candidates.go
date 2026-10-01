package discover

import (
	"net"
	"sort"
	"strconv"
)

// CandidateAddrs 返回本机块交换数据面可被对端拨入的候选 ip:port 列表。
//
// 枚举活动、非虚拟、持有 RFC1918 私网 IPv4（排除 loopback 与 CGNAT）的网卡，
// 配合对 0.0.0.0:port 的通配监听即可在各网卡上接受连接。结果排序保证确定性，
// 便于调用方按序做 Happy Eyeballs 拨号与测试断言。
func CandidateAddrs(port int) []string {
	var out []string
	seen := map[string]bool{}
	for _, ifi := range multicastIfaces() {
		if isVirtualIface(ifi.Name) {
			continue
		}
		for _, ip := range ifacePrivateIPv4s(ifi) {
			addr := net.JoinHostPort(ip, strconv.Itoa(port))
			if seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, addr)
		}
	}
	sort.Strings(out)
	return out
}

// ifacePrivateIPv4s 返回网卡上可用的私网 IPv4 地址（排除 loopback 与 CGNAT）。
func ifacePrivateIPv4s(ifi net.Interface) []string {
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		v4 := ip.To4()
		if v4 == nil || isCGNAT(v4) || !v4.IsPrivate() || v4.IsLoopback() {
			continue
		}
		out = append(out, v4.String())
	}
	return out
}
