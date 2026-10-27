package discover

import (
	"context"
	"encoding/json"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	udpBeaconQueryMagic = "LANCHAT_DISCOVER_V1?"
	udpBeaconReplyMagic = "LANCHAT_DISCOVER_V1!"
)

// DefaultBeaconPorts 是 LANChat 专用 UDP 局域网发现端口组（避开被 adb/avahi 占用的 5353）。
// 同一台机器启动多个实例时自动顺延绑定空闲端口，探测端并发向全部端口广播/单播。
var DefaultBeaconPorts = []int{7879, 7880, 7881, 19091}

type udpBeaconReply struct {
	Magic   string   `json:"magic"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Auth    string   `json:"auth"`
	Version string   `json:"version"`
	TLS     bool     `json:"tls"`
	Port    int      `json:"port"`
	IPs     []string `json:"ips"`
}

// startUDPBeacon 启动 UDP 广播/单播发现响应器，返回关闭函数。
func startUDPBeacon(ctx context.Context, servicePort int, meta ServerMeta) func() {
	var conns []*net.UDPConn
	for _, bp := range DefaultBeaconPorts {
		addr := &net.UDPAddr{IP: net.IPv4zero, Port: bp}
		conn, err := net.ListenUDP("udp4", addr)
		if err != nil {
			continue
		}
		conns = append(conns, conn)
		go serveUDPBeaconConn(ctx, conn, servicePort, meta)
		// 绑定首个可用端口即可满足本实例响应，避免单实例占满全部候选端口
		break
	}
	return func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}
}

func serveUDPBeaconConn(ctx context.Context, conn *net.UDPConn, servicePort int, meta ServerMeta) {
	buf := make([]byte, 2048)
	for {
		if ctx.Err() != nil {
			return
		}
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if msg != udpBeaconQueryMagic {
			continue
		}
		reply := udpBeaconReply{
			Magic:   udpBeaconReplyMagic,
			ID:      meta.ID,
			Name:    meta.Name,
			Path:    meta.Path,
			Auth:    meta.Auth,
			Version: meta.Version,
			TLS:     meta.TLS,
			Port:    servicePort,
			IPs:     localAdvertisedIPv4s(),
		}
		if reply.Path == "" {
			reply.Path = "/lanchat"
		}
		data, err := json.Marshal(reply)
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(data, remoteAddr)
	}
}

func localAdvertisedIPv4s() []string {
	seen := map[string]bool{}
	var out []string
	for _, hp := range CandidateAddrs(0) {
		if h, _, err := net.SplitHostPort(hp); err == nil && h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, hp := range OverlayCandidateAddrs(0) {
		if h, _, err := net.SplitHostPort(hp); err == nil && h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// ProbeUDP 通过 UDP 广播（255.255.255.255 + 各网卡定向广播）与本机回环发送发现探测包，
// 收集局域网内所有存活 LANChat 节点的单播应答（不依赖 5353 mDNS 多播）。
func ProbeUDP(ctx context.Context, timeout time.Duration, extraHosts ...string) []Server {
	if timeout <= 0 {
		timeout = 160 * time.Millisecond
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil
	}
	defer conn.Close()

	targets := broadcastAndLocalTargets(extraHosts...)
	payload := []byte(udpBeaconQueryMagic)
	for _, ip := range targets {
		for _, port := range DefaultBeaconPorts {
			_, _ = conn.WriteToUDP(payload, &net.UDPAddr{IP: ip, Port: port})
		}
	}

	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)

	byKey := map[string]*Server{}
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			break
		}
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		var rep udpBeaconReply
		if err := json.Unmarshal(buf[:n], &rep); err != nil {
			continue
		}
		if rep.Magic != udpBeaconReplyMagic || rep.Port <= 0 {
			continue
		}
		key := rep.ID
		if key == "" && remoteAddr != nil {
			key = remoteAddr.IP.String()
		}
		srv, ok := byKey[key]
		if !ok {
			srv = &Server{
				ID:      rep.ID,
				Name:    rep.Name,
				Path:    rep.Path,
				Auth:    rep.Auth,
				TLS:     rep.TLS,
				Version: rep.Version,
				Port:    rep.Port,
			}
			byKey[key] = srv
		}
		// 若应答来自非回环远端 IP，优先采用远端真实源 IP
		if remoteAddr != nil {
			if v4 := remoteAddr.IP.To4(); v4 != nil && !v4.IsLoopback() && !containsIP(srv.Addresses, v4) {
				srv.Addresses = append(srv.Addresses, v4)
			}
		}
		for _, rawIP := range rep.IPs {
			if ip := net.ParseIP(strings.TrimSpace(rawIP)); ip != nil {
				if v4 := ip.To4(); v4 != nil && !containsIP(srv.Addresses, v4) {
					srv.Addresses = append(srv.Addresses, v4)
				}
			}
		}
		if len(srv.Addresses) == 0 && remoteAddr != nil {
			if v4 := remoteAddr.IP.To4(); v4 != nil {
				srv.Addresses = append(srv.Addresses, v4)
			}
		}
	}

	out := make([]Server, 0, len(byKey))
	for _, s := range byKey {
		if len(s.Addresses) == 0 {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func broadcastAndLocalTargets(extraHosts ...string) []net.IP {
	seen := map[string]bool{}
	var out []net.IP
	add := func(ip net.IP) {
		v4 := ip.To4()
		if v4 == nil {
			return
		}
		s := v4.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, v4)
		}
	}

	add(net.IPv4(127, 0, 0, 1))
	add(net.IPv4bcast)

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, ifi := range ifaces {
			if (ifi.Flags&net.FlagUp) == 0 || (ifi.Flags&net.FlagLoopback) != 0 {
				continue
			}
			addrs, err := ifi.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				ip, ipNet, err := net.ParseCIDR(a.String())
				if err != nil || ipNet == nil {
					continue
				}
				v4 := ip.To4()
				mask := ipNet.Mask
				if v4 == nil || len(mask) != 4 {
					continue
				}
				// 计算子网定向广播地址: ip | ^mask
				bcast := net.IPv4(
					v4[0]|^mask[0],
					v4[1]|^mask[1],
					v4[2]|^mask[2],
					v4[3]|^mask[3],
				)
				if !bcast.Equal(v4) {
					add(bcast)
				}
			}
		}
	}

	for _, h := range extraHosts {
		clean := strings.TrimSpace(h)
		if host, _, err := net.SplitHostPort(clean); err == nil {
			clean = host
		}
		if ip := net.ParseIP(clean); ip != nil {
			add(ip)
		}
	}
	return out
}
