package discover

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const gossipFileName = "gossip_peers.json"

// OverlayCandidateAddrs 返回本机在 Tailscale（100.64.0.0/10 CGNAT）、WireGuard（wg*）
// 及跨 VLAN 私网接口上的可拨入 ip:port 列表（阶段四 v2.0 跨网段协同）。
//
// 注意：mDNS 广告（lanInterfaces）仍仅绑定物理局域网网卡以避免 dnssd 多网卡多播环回洪泛；
// 跨 VLAN / Tailscale 节点发现与 P2P 拨号通过本函数及主动探测（ProbeServers / Gossip）完成。
func OverlayCandidateAddrs(port int) []string {
	var out []string
	seen := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags&net.FlagUp) == 0 || (ifi.Flags&net.FlagLoopback) != 0 {
			continue
		}
		for _, ip := range ifaceOverlayOrPrivateIPv4s(ifi) {
			addr := net.JoinHostPort(ip, strconv.Itoa(port))
			if !seen[addr] {
				seen[addr] = true
				out = append(out, addr)
			}
		}
	}
	sort.Strings(out)
	return out
}

func ifaceOverlayOrPrivateIPv4s(ifi net.Interface) []string {
	lower := strings.ToLower(ifi.Name)
	for _, skip := range []string{"lo", "docker", "veth", "br-", "virbr", "cni", "flannel"} {
		if strings.HasPrefix(lower, skip) {
			return nil
		}
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			if parsed := net.ParseIP(strings.TrimSpace(addr.String())); parsed != nil {
				ip = parsed
			} else {
				continue
			}
		}
		v4 := ip.To4()
		if v4 == nil || v4.IsLoopback() {
			continue
		}
		if v4.IsPrivate() || isCGNAT(v4) {
			out = append(out, v4.String())
		}
	}
	return out
}

// ProbeServers 对一组跨网段 / Tailscale / Gossip 候选地址并发执行 TCP 探测，
// 返回可达的中心节点列表（用于补充 mDNS 无法穿透的三层子网与 VPN 网段，阶段四 v2.0）。
func ProbeServers(ctx context.Context, endpoints []string, defaultPort int, timeout time.Duration) []Server {
	if len(endpoints) == 0 {
		return nil
	}
	if defaultPort <= 0 {
		defaultPort = 7878
	}
	if timeout <= 0 {
		timeout = 600 * time.Millisecond
	}

	type probeHit struct {
		host string
		port int
		ip   net.IP
	}
	var (
		mu   sync.Mutex
		hits []probeHit
		wg   sync.WaitGroup
		seen = map[string]bool{}
	)

	for _, raw := range endpoints {
		ep := strings.TrimSpace(raw)
		if ep == "" {
			continue
		}
		host, portStr, err := net.SplitHostPort(ep)
		port := defaultPort
		if err != nil {
			host = ep
		} else if p, pErr := strconv.Atoi(portStr); pErr == nil && p > 0 {
			port = p
		}
		hostPort := net.JoinHostPort(host, strconv.Itoa(port))
		if seen[hostPort] {
			continue
		}
		seen[hostPort] = true

		wg.Add(1)
		go func(h string, p int, hp string) {
			defer wg.Done()
			if !reachable(ctx, hp, timeout) {
				return
			}
			ips := resolveHostIPs(h)
			var v4 net.IP
			if len(ips) > 0 {
				v4 = ips[0]
			}
			mu.Lock()
			hits = append(hits, probeHit{host: h, port: p, ip: v4})
			mu.Unlock()
		}(host, port, hostPort)
	}
	wg.Wait()

	out := make([]Server, 0, len(hits))
	for _, hit := range hits {
		var addrs []net.IP
		if hit.ip != nil {
			addrs = []net.IP{hit.ip}
		}
		out = append(out, Server{
			ID:        "overlay-" + hit.host + ":" + strconv.Itoa(hit.port),
			Name:      "跨网段节点 (" + hit.host + ")",
			Path:      "/lctp",
			Auth:      "psk",
			Port:      hit.port,
			Addresses: addrs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// GossipPeerBook 维护跨 VLAN / Tailscale 已知节点地址簿（支持 Gossip 交换与本地持久化）。
type GossipPeerBook struct {
	mu    sync.Mutex
	peers map[string]int64 // host:port -> unixMilli
	path  string
}

// NewGossipPeerBook 创建并加载本地 Gossip 节点地址簿。
func NewGossipPeerBook(customPath string) *GossipPeerBook {
	if customPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			customPath = filepath.Join(home, ".lanchat", gossipFileName)
		}
	}
	b := &GossipPeerBook{
		peers: make(map[string]int64),
		path:  customPath,
	}
	b.load()
	return b
}

// Add 记录一个或多个跨网段候选地址（如 100.93.183.47:7878）。
func (b *GossipPeerBook) Add(endpoints ...string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	added := 0
	now := time.Now().UnixMilli()
	for _, ep := range endpoints {
		clean := strings.TrimSpace(ep)
		if clean == "" {
			continue
		}
		if _, exists := b.peers[clean]; !exists {
			added++
		}
		b.peers[clean] = now
	}
	if added > 0 {
		b.saveLocked()
	}
	return added
}

// SeedTailscalePeers 自动读取 tailscale status --json 中在线对端的 Tailscale IP 并注入 GossipPeerBook（v4.7.0）。
func (b *GossipPeerBook) SeedTailscalePeers(defaultPort int) int {
	if defaultPort <= 0 {
		defaultPort = 7878
	}
	ips := TailscalePeerIPs()
	if len(ips) == 0 {
		return 0
	}
	eps := make([]string, 0, len(ips))
	for _, ip := range ips {
		eps = append(eps, net.JoinHostPort(ip, strconv.Itoa(defaultPort)))
	}
	return b.Add(eps...)
}

// Endpoints 返回按字典序排列的全部已知跨网段节点地址。
func (b *GossipPeerBook) Endpoints() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.peers))
	for ep := range b.peers {
		out = append(out, ep)
	}
	sort.Strings(out)
	return out
}

func (b *GossipPeerBook) load() {
	if b.path == "" {
		return
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return
	}
	var saved map[string]int64
	if json.Unmarshal(raw, &saved) == nil {
		for k, v := range saved {
			b.peers[k] = v
		}
	}
}

func (b *GossipPeerBook) saveLocked() {
	if b.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(b.path), 0o700)
	if raw, err := json.MarshalIndent(b.peers, "", "  "); err == nil {
		_ = os.WriteFile(b.path, raw, 0o600)
	}
}

// MultipathRouteMetric 描述单条跨网卡/跨子网候选链路的探测指标与动态调度权重（v7.3.0）。
type MultipathRouteMetric struct {
	Endpoint  string        `json:"endpoint"`
	LinkType  string        `json:"linkType"` // "lan" | "tailscale" | "overlay"
	RTT       time.Duration `json:"rtt"`
	Weight    int           `json:"weight"`
	Reachable bool          `json:"reachable"`
}

// ClassifyEndpointLinkType 根据候选地址 IP 识别链路类型（lan / tailscale / overlay，v7.3.0）。
func ClassifyEndpointLinkType(endpoint string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		host = strings.TrimSpace(endpoint)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "overlay"
	}
	v4 := ip.To4()
	if v4 == nil {
		return "overlay"
	}
	if isCGNAT(v4) {
		return "tailscale"
	}
	if v4.IsLoopback() || v4.IsPrivate() {
		return "lan"
	}
	return "overlay"
}

// ProbeMultipathRoutes 并发探测有线局域网、Wi-Fi 与 Tailscale 候选地址，按实测 RTT 与链路层级计算聚合调度权重（v7.3.0）。
func ProbeMultipathRoutes(ctx context.Context, endpoints []string, timeout time.Duration) []MultipathRouteMetric {
	if len(endpoints) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	seen := make(map[string]bool, len(endpoints))
	unique := make([]string, 0, len(endpoints))
	for _, raw := range endpoints {
		ep := strings.TrimSpace(raw)
		if ep == "" || seen[ep] {
			continue
		}
		seen[ep] = true
		unique = append(unique, ep)
	}

	results := make([]MultipathRouteMetric, len(unique))
	var wg sync.WaitGroup
	for i, ep := range unique {
		wg.Add(1)
		go func(idx int, target string) {
			defer wg.Done()
			linkType := ClassifyEndpointLinkType(target)
			start := time.Now()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", target)
			rtt := time.Since(start)
			if err != nil {
				results[idx] = MultipathRouteMetric{
					Endpoint:  target,
					LinkType:  linkType,
					RTT:       timeout,
					Weight:    0,
					Reachable: false,
				}
				return
			}
			_ = conn.Close()
			weight := computeRouteWeight(linkType, rtt)
			results[idx] = MultipathRouteMetric{
				Endpoint:  target,
				LinkType:  linkType,
				RTT:       rtt,
				Weight:    weight,
				Reachable: true,
			}
		}(i, ep)
	}
	wg.Wait()

	out := make([]MultipathRouteMetric, 0, len(results))
	for _, r := range results {
		if r.Reachable {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].RTT < out[j].RTT
	})
	return out
}

func computeRouteWeight(linkType string, rtt time.Duration) int {
	var base int
	switch linkType {
	case "lan":
		base = 4
	case "tailscale":
		base = 2
	default:
		base = 1
	}
	switch {
	case rtt <= 2*time.Millisecond:
		return base + 4
	case rtt <= 10*time.Millisecond:
		return base + 2
	case rtt <= 40*time.Millisecond:
		return base + 1
	default:
		return base
	}
}
