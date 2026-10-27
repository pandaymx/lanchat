package core

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
)

var (
	embeddedRegistryMu sync.Mutex
	embeddedRegistry   = map[string]appapi.Server{}
)

func registerEmbeddedServer(srv appapi.Server) {
	embeddedRegistryMu.Lock()
	embeddedRegistry[srv.ID] = srv
	embeddedRegistryMu.Unlock()
}

func unregisterEmbeddedServer(id string) {
	embeddedRegistryMu.Lock()
	delete(embeddedRegistry, id)
	embeddedRegistryMu.Unlock()
}

func snapshotEmbeddedServers() []appapi.Server {
	embeddedRegistryMu.Lock()
	list := make([]appapi.Server, 0, len(embeddedRegistry))
	for _, s := range embeddedRegistry {
		list = append(list, s)
	}
	embeddedRegistryMu.Unlock()

	var live []appapi.Server
	for _, s := range list {
		if s.Addr == "" {
			continue
		}
		if conn, err := net.DialTimeout("tcp", s.Addr, 150*time.Millisecond); err == nil {
			_ = conn.Close()
			live = append(live, s)
		} else {
			unregisterEmbeddedServer(s.ID)
		}
	}
	return live
}

// BrowseServers 返回当前 mDNS + UDP 广播发现到的中心节点，并合并内嵌自举节点与
// LANCHAT_STATIC_SERVERS / Gossip 配置的跨网段候选中心节点；
// 当 AutoStandby 开启且全网尚未发现任何存活节点时，自动在本机拉起待命内嵌节点。
func (c *Client) BrowseServers() []appapi.Server {
	c.mu.Lock()
	b := c.browser
	if b == nil {
		b = discover.NewBrowser()
		c.browser = b
	}
	autoStandby := c.autoStandby
	psk := c.psk
	c.mu.Unlock()

	if c.browserStarted.CompareAndSwap(false, true) {
		go func() { _ = b.Browse(c.runCtx, func([]discover.Server) {}) }()
	}

	entries := b.Snapshot()
	seenAddr := make(map[string]bool, len(entries)+4)
	seenID := make(map[string]bool, len(entries)+4)
	out := make([]appapi.Server, 0, len(entries)+4)
	addSrv := func(srv appapi.Server) {
		if srv.Addr == "" {
			return
		}
		if seenAddr[srv.Addr] || (srv.ID != "" && seenID[srv.ID]) {
			return
		}
		seenAddr[srv.Addr] = true
		if srv.ID != "" {
			seenID[srv.ID] = true
		}
		out = append(out, srv)
	}

	for _, s := range entries {
		addSrv(toAPIServer(s))
	}

	for _, s := range snapshotEmbeddedServers() {
		addSrv(s)
	}

	gossipEps := c.GossipEndpoints()
	if autoStandby {
		for _, s := range discover.ProbeUDP(c.runCtx, 180*time.Millisecond, gossipEps...) {
			addSrv(toAPIServer(s))
		}
	}

	staticRaw := os.Getenv("LANCHAT_STATIC_SERVERS")
	if len(gossipEps) > 0 {
		if staticRaw != "" {
			staticRaw += "," + strings.Join(gossipEps, ",")
		} else {
			staticRaw = strings.Join(gossipEps, ",")
		}
	}
	for _, s := range probeStaticServers(staticRaw) {
		addSrv(s)
	}

	if autoStandby {
		for _, hp := range []string{"127.0.0.1:7878", "127.0.0.1:19090"} {
			if srv, ok := probeHTTPStatusOnly(hp); ok {
				addSrv(srv)
			}
		}
	}

	// 若处于生产客户端模式（autoStandby）且局域网尚未发现任何中心节点，自动在本机拉起待命内嵌节点
	if len(out) == 0 && autoStandby {
		c.mu.Lock()
		noEmbedded := c.embeddedStop == nil
		c.mu.Unlock()
		if noEmbedded {
			if _, err := c.startEmbeddedServer(psk); err == nil {
				for _, s := range snapshotEmbeddedServers() {
					addSrv(s)
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func probeHTTPStatusOnly(hostPort string) (appapi.Server, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	client := &http.Client{Timeout: 150 * time.Millisecond}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort+"/api/status", nil)
	if err != nil {
		return appapi.Server{}, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return appapi.Server{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appapi.Server{}, false
	}
	var st statusProbeResponse
	if json.NewDecoder(resp.Body).Decode(&st) != nil {
		return appapi.Server{}, false
	}
	ver := st.ProtocolVersion
	if ver == "" {
		ver = protocol.ProtocolVersion
	}
	auth := st.AuthMode
	if auth == "" {
		auth = "none"
	}
	return appapi.Server{
		Name:     "本机中心节点 (" + hostPort + ")",
		ID:       "local-" + hostPort,
		Addr:     hostPort,
		Version:  ver,
		AuthMode: auth,
	}, true
}

// toAPIServer 把 discover 记录映射为契约结构；地址取首个 IPv4:Port。
func toAPIServer(s discover.Server) appapi.Server {
	addr := ""
	if len(s.Addresses) > 0 {
		addr = net.JoinHostPort(s.Addresses[0].String(), strconv.Itoa(s.Port))
	}
	return appapi.Server{
		Name:     s.Name,
		ID:       s.ID,
		Addr:     addr,
		Version:  s.Version,
		AuthMode: s.Auth,
	}
}

type statusProbeResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	AuthMode        string `json:"authMode"`
}

// probeStaticServers 并发探测逗号分隔的静态服务器候选（如 "10.10.1.5:19090,10.20.1.5:19090"）。
func probeStaticServers(raw string) []appapi.Server {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var candidates []string
	seen := map[string]bool{}
	for _, p := range parts {
		addr := normalizeStaticAddr(p)
		if addr != "" && !seen[addr] {
			seen[addr] = true
			candidates = append(candidates, addr)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []appapi.Server
	)
	client := &http.Client{Timeout: 400 * time.Millisecond}
	for _, addr := range candidates {
		wg.Add(1)
		go func(hostPort string) {
			defer wg.Done()
			srv, ok := probeOneStaticServer(client, hostPort)
			if !ok {
				return
			}
			mu.Lock()
			out = append(out, srv)
			mu.Unlock()
		}(addr)
	}
	wg.Wait()
	return out
}

func normalizeStaticAddr(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return ""
		}
		s = u.Host
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		return ""
	}
	return s
}

func probeOneStaticServer(client *http.Client, hostPort string) (appapi.Server, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort+"/api/status", nil)
	if err == nil {
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var st statusProbeResponse
				if json.NewDecoder(resp.Body).Decode(&st) == nil {
					ver := st.ProtocolVersion
					if ver == "" {
						ver = protocol.ProtocolVersion
					}
					auth := st.AuthMode
					if auth == "" {
						auth = "psk"
					}
					return appapi.Server{
						Name:     "静态节点 (" + hostPort + ")",
						ID:       "static-" + hostPort,
						Addr:     hostPort,
						Version:  ver,
						AuthMode: auth,
					}, true
				}
			}
		}
	}

	// 回退 TCP 探测，兼容未开启 /api/status 的旧版服务器。
	conn, err := net.DialTimeout("tcp", hostPort, 300*time.Millisecond)
	if err != nil {
		return appapi.Server{}, false
	}
	_ = conn.Close()
	return appapi.Server{
		Name:     "静态节点 (" + hostPort + ")",
		ID:       "static-" + hostPort,
		Addr:     hostPort,
		Version:  protocol.ProtocolVersion,
		AuthMode: "psk",
	}, true
}
