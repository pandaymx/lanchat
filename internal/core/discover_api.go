package core

import (
	"net"
	"sort"
	"strconv"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/discover"
)

// BrowseServers 返回当前 mDNS 浏览到的中心节点；首次调用时懒启动常驻浏览。
// dnssd 是持续监听模型，重扫即读取最新快照（后续条目经 onUpdate 自动更新）。
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
	out := make([]appapi.Server, 0, len(entries))
	for _, s := range entries {
		out = append(out, toAPIServer(s))
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
