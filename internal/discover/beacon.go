package discover

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	udpBeaconQueryMagic = "LANCHAT_DISCOVER_V1?"
	udpBeaconReplyMagic = "LANCHAT_DISCOVER_V1!"
)

var (
	tailscaleCacheMu        sync.Mutex
	tailscaleCachedIPs      []string
	tailscaleCacheExpiresAt time.Time
	tailscaleStatusProvider func() ([]byte, error)
)

type tailscaleStatusJSON struct {
	BackendState string                       `json:"BackendState"`
	TailscaleIPs []string                     `json:"TailscaleIPs"`
	Self         *tailscalePeerNode           `json:"Self"`
	Peer         map[string]tailscalePeerNode `json:"Peer"`
}

type tailscalePeerNode struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
	Active       bool     `json:"Active"`
}

// ParseTailscaleStatusJSON 解析 tailscale status --json（或 localapi /localapi/v0/status）输出，
// 提取所有在线对端节点的 Tailscale IPv4 地址（100.64.0.0/10 CGNAT），排除本机自身 IP。
func ParseTailscaleStatusJSON(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var st tailscaleStatusJSON
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil
	}
	if st.BackendState != "" && st.BackendState != "Running" {
		return nil
	}
	selfIPs := map[string]bool{}
	for _, rawIP := range st.TailscaleIPs {
		if ip := net.ParseIP(strings.TrimSpace(rawIP)); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				selfIPs[v4.String()] = true
			}
		}
	}
	if st.Self != nil {
		for _, rawIP := range st.Self.TailscaleIPs {
			if ip := net.ParseIP(strings.TrimSpace(rawIP)); ip != nil {
				if v4 := ip.To4(); v4 != nil {
					selfIPs[v4.String()] = true
				}
			}
		}
	}

	seen := map[string]bool{}
	var out []string
	for _, peer := range st.Peer {
		if !peer.Online && !peer.Active {
			continue
		}
		for _, rawIP := range peer.TailscaleIPs {
			ip := net.ParseIP(strings.TrimSpace(rawIP))
			if ip == nil {
				continue
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || !isCGNAT(v4) {
				continue
			}
			s := v4.String()
			if selfIPs[s] || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TailscalePeerIPs 通过本地 tailscaled Unix 套接字或 tailscale status --json CLI
// 自动读取当前 Tailscale 虚拟网中所有在线节点的 IPv4 地址（带 15s 内存缓存，v4.7.0）。
func TailscalePeerIPs() []string {
	tailscaleCacheMu.Lock()
	if tailscaleStatusProvider == nil && isDiscoverTestBinary() {
		tailscaleCacheMu.Unlock()
		return nil
	}
	if time.Now().Before(tailscaleCacheExpiresAt) && tailscaleStatusProvider == nil {
		cp := append([]string(nil), tailscaleCachedIPs...)
		tailscaleCacheMu.Unlock()
		return cp
	}
	provider := tailscaleStatusProvider
	tailscaleCacheMu.Unlock()

	var (
		raw []byte
		err error
	)
	if provider != nil {
		raw, err = provider()
	} else {
		raw, err = fetchTailscaleStatusRaw()
	}
	if err != nil || len(raw) == 0 {
		return nil
	}
	ips := ParseTailscaleStatusJSON(raw)

	tailscaleCacheMu.Lock()
	tailscaleCachedIPs = ips
	tailscaleCacheExpiresAt = time.Now().Add(15 * time.Second)
	tailscaleCacheMu.Unlock()

	return append([]string(nil), ips...)
}

func isDiscoverTestBinary() bool {
	if os.Getenv("LANCHAT_DISABLE_TAILSCALE") != "" {
		return true
	}
	exe := filepath.Base(os.Args[0])
	return strings.HasSuffix(exe, ".test") || strings.HasSuffix(exe, ".test.exe") || strings.Contains(exe, "_test")
}

func fetchTailscaleStatusRaw() ([]byte, error) {
	for _, sock := range []string{"/var/run/tailscale/tailscaled.sock", "/run/tailscale/tailscaled.sock"} {
		if st, err := os.Stat(sock); err == nil && !st.IsDir() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			tr := &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sock)
				},
			}
			req, rErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscaled.sock/localapi/v0/status", nil)
			if rErr == nil {
				client := &http.Client{Transport: tr, Timeout: 120 * time.Millisecond}
				resp, dErr := client.Do(req)
				if dErr == nil {
					body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
					_ = resp.Body.Close()
					cancel()
					if readErr == nil && resp.StatusCode == http.StatusOK && len(body) > 0 {
						return body, nil
					}
				}
			}
			cancel()
		}
	}

	path, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	return exec.CommandContext(ctx, path, "status", "--json").Output()
}

// IsTailscaleIP 判断给定的 IP、host:port 或 ws:// URL 是否属于 Tailscale 100.64.0.0/10 网段（v4.7.0）。
func IsTailscaleIP(addrOrIP string) bool {
	s := strings.TrimSpace(addrOrIP)
	if s == "" {
		return false
	}
	for _, prefix := range []string{"ws://", "wss://", "http://", "https://"} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			s = s[len(prefix):]
			break
		}
	}
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	return v4 != nil && isCGNAT(v4)
}

// LinkModeLabel 返回地址对应的 UI 链路标识文案（"⚡ 局域网直连" 或 "🌐 Tailscale 直连"）。
func LinkModeLabel(addrOrIP string) string {
	if IsTailscaleIP(addrOrIP) {
		return "🌐 Tailscale 直连"
	}
	return "⚡ 局域网直连"
}

// PrioritizeDualStackAddresses 按「同子网物理局域网 IP > Tailscale CGNAT IP > 其他私网 IP > 回环」
// 对同一节点的双栈候选 IPv4 列表进行稳定排序（v4.7.0）。
func PrioritizeDualStackAddresses(addrs []net.IP) []net.IP {
	if len(addrs) <= 1 {
		return append([]net.IP(nil), addrs...)
	}
	localLAN := localPhysicalLANSubnets()
	return prioritizeDualStackWithSubnets(addrs, localLAN)
}

func prioritizeDualStackWithSubnets(addrs []net.IP, localLAN []*net.IPNet) []net.IP {
	out := make([]net.IP, 0, len(addrs))
	seen := map[string]bool{}
	for _, ip := range addrs {
		if v4 := ip.To4(); v4 != nil {
			s := v4.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, v4)
			}
		}
	}
	tierOf := func(v4 net.IP) int {
		if v4.IsLoopback() {
			return 0
		}
		if !isCGNAT(v4) && v4.IsPrivate() {
			for _, n := range localLAN {
				if n.Contains(v4) {
					return 4 // 同子网物理网卡 IP（千兆/万兆低延迟）最高优
				}
			}
			return 2 // 跨子网私网 IP（无路由时不可达，排在 Tailscale 之后）
		}
		if isCGNAT(v4) {
			return 3 // Tailscale 100.64.0.0/10 跨网段直连次优
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := tierOf(out[i]), tierOf(out[j])
		if ti != tj {
			return ti > tj
		}
		return ipLess(out[i], out[j])
	})
	return out
}

func localPhysicalLANSubnets() []*net.IPNet {
	var nets []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags&net.FlagUp) == 0 || (ifi.Flags&net.FlagLoopback) != 0 || isVirtualIface(ifi.Name) {
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
			if v4 == nil || v4.IsLoopback() || isCGNAT(v4) || !v4.IsPrivate() {
				continue
			}
			nets = append(nets, ipNet)
			mask24 := net.CIDRMask(24, 32)
			nets = append(nets, &net.IPNet{IP: v4.Mask(mask24), Mask: mask24})
		}
	}
	return nets
}

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
	for ctx.Err() == nil {
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
	for ctx.Err() == nil {
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
		s.Addresses = PrioritizeDualStackAddresses(s.Addresses)
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

	for _, tsIP := range TailscalePeerIPs() {
		if ip := net.ParseIP(tsIP); ip != nil {
			add(ip)
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
