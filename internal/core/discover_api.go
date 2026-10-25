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

// BrowseServers 返回当前 mDNS 浏览到的中心节点，并合并 LANCHAT_STATIC_SERVERS
// 配置的跨网段静态候选中心节点；首次调用时懒启动常驻浏览。
func (c *Client) BrowseServers() []appapi.Server {
	c.mu.Lock()
	b := c.browser
	if b == nil {
		b = discover.NewBrowser()
		c.browser = b
	}
	c.mu.Unlock()

	if c.browserStarted.CompareAndSwap(false, true) {
		go func() { _ = b.Browse(c.runCtx, func([]discover.Server) {}) }()
	}

	entries := b.Snapshot()
	seenAddr := make(map[string]bool, len(entries))
	out := make([]appapi.Server, 0, len(entries))
	for _, s := range entries {
		srv := toAPIServer(s)
		if srv.Addr != "" {
			seenAddr[srv.Addr] = true
		}
		out = append(out, srv)
	}

	for _, s := range probeStaticServers(os.Getenv("LANCHAT_STATIC_SERVERS")) {
		if !seenAddr[s.Addr] {
			seenAddr[s.Addr] = true
			out = append(out, s)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
