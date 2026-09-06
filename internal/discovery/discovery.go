// Package discovery 实现局域网 mDNS/DNS-SD 服务发现：
// hub 启动时 Broadcast 注册 _lanchat._tcp 服务，client 启动时 Discover
// 自动找到局域网内的 hub，免去手输 IP（目录规划见 AGENTS.md §4）。
//
// 纯 Go 实现（grandcat/zeroconf + miekg/dns），零 CGO，不影响交叉编译。
// 多播在无组播路由的环境（部分容器/CI）可能收不到——调用方应把
// "发现不到" 当作可降级路径（提示用户用 -hub 显式指定）。
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// ErrNoHub 表示浏览超时内没有发现任何 lanchat hub。
var ErrNoHub = errors.New("discovery: 局域网内未发现 lanchat hub（可用 -hub 显式指定地址）")

const (
	// ServiceType 是 lanchat hub 在 DNS-SD 里注册的服务类型。
	ServiceType = "_lanchat._tcp"
	// ServiceDomain 用本地链路域名。
	ServiceDomain = "local."
	// InstanceName 是注册的默认实例名。
	InstanceName = "lanchat-hub"

	// MetaPath 是 TXT 记录里 WS upgrade 路径的 key。
	MetaPath = "path"
	// MetaVersion 是 TXT 记录里 hub 版本的 key。
	MetaVersion = "version"

	// DefaultWSPath 是 TXT 缺失 path 时的回退值，与 ws.DefaultPath 对齐。
	DefaultWSPath = "/ws"

	// discoverGrace 是收到首个实例后继续收集同网段其它 hub 的宽限时间。
	discoverGrace = 800 * time.Millisecond
)

// Instance 是一个发现到的 hub 实例。
type Instance struct {
	// Name 是 DNS-SD 实例名（如 lanchat-hub）。
	Name string
	// Host 是实例主机名。
	Host string
	// Addr 是优选可达地址（优先非 loopback IPv4，回退 IPv6）。
	Addr string
	// Port 是 hub 监听端口。
	Port int
	// Path 是 WebSocket upgrade 路径，TXT 缺失时为 DefaultWSPath。
	Path string
}

// Broadcast 在局域网内注册 hub 的 mDNS 服务，返回 shutdown 用于注销。
// 注册在后台持续发送，shutdown 阻塞直到注销完成。
//
// instance 为空时用 InstanceName；meta 里可带 MetaPath/MetaVersion。
func Broadcast(instance string, port int, meta map[string]string) (shutdown func(), err error) {
	if instance == "" {
		instance = InstanceName
	}
	text := make([]string, 0, len(meta))
	for k, v := range meta {
		text = append(text, k+"="+v)
	}
	srv, err := zeroconf.Register(instance, ServiceType, ServiceDomain, port, text, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: register %s: %w", ServiceType, err)
	}
	return srv.Shutdown, nil
}

// Discover 在局域网内浏览 lanchat hub，timeout 后返回去重后的实例列表
// （按实例名排序，结果稳定便于测试与「取第一个」）。
//
// 超时内一个都没发现时返回 nil + nil（不报错——环境可能无组播，
// 由调用方决定如何提示）。
func Discover(ctx context.Context, timeout time.Duration) ([]Instance, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: new resolver: %w", err)
	}

	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry)
	var found []*zeroconf.ServiceEntry
	seen := make(map[string]bool)
	collectDone := make(chan struct{})
	firstEntry := make(chan struct{}, 1)
	go func() {
		defer close(collectDone)
		for e := range entries {
			if e == nil || seen[e.Instance] {
				continue
			}
			seen[e.Instance] = true
			found = append(found, e)
			select {
			case firstEntry <- struct{}{}:
			default:
			}
		}
	}()

	if err := resolver.Browse(browseCtx, ServiceType, ServiceDomain, entries); err != nil {
		return nil, fmt.Errorf("discovery: browse %s: %w", ServiceType, err)
	}
	// 不必干等满 timeout：收到首个实例后再留 discoverGrace 收集同网段
	// 其它 hub（多实例场景），然后提前结束浏览；一个都没发现则等满超时。
	select {
	case <-browseCtx.Done():
	case <-firstEntry:
		grace := time.NewTimer(discoverGrace)
		select {
		case <-browseCtx.Done():
		case <-grace.C:
			cancel()
		}
	}
	<-collectDone

	out := make([]Instance, 0, len(found))
	for _, e := range found {
		addr := pickAddr(e)
		if addr == "" {
			continue // 没有任何可用地址的条目跳过
		}
		path := textValue(e.Text, MetaPath)
		if path == "" {
			path = DefaultWSPath
		}
		out = append(out, Instance{
			Name: e.Instance,
			Host: e.HostName,
			Addr: addr,
			Port: e.Port,
			Path: path,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DiscoverHubURL 发现局域网内第一个 hub 实例，返回可直接拨号的 ws:// URL。
// 超时未发现返回 ErrNoHub（调用方提示用户用 -hub / -hub-url 显式指定）。
func DiscoverHubURL(ctx context.Context, timeout time.Duration) (string, error) {
	instances, err := Discover(ctx, timeout)
	if err != nil {
		return "", err
	}
	if len(instances) == 0 {
		return "", ErrNoHub
	}
	first := instances[0]
	return "ws://" + net.JoinHostPort(first.Addr, strconv.Itoa(first.Port)) + first.Path, nil
}

// textValue 从 DNS-SD TXT 记录（"k=v" 切片）里取 key 对应值。
func textValue(text []string, key string) string {
	prefix := key + "="
	for _, t := range text {
		if v, ok := strings.CutPrefix(t, prefix); ok {
			return v
		}
	}
	return ""
}

// pickAddr 从 ServiceEntry 里选一个首选地址：非 loopback 的 IPv4 优先，
// 其次非 loopback IPv6；都没有时允许 loopback（本机自测）。
func pickAddr(e *zeroconf.ServiceEntry) string {
	for _, ip := range e.AddrIPv4 {
		if !ip.IsLoopback() {
			return ip.String()
		}
	}
	for _, ip := range e.AddrIPv6 {
		if !ip.IsLoopback() {
			return ip.String()
		}
	}
	for _, ip := range e.AddrIPv4 {
		return ip.String()
	}
	for _, ip := range e.AddrIPv6 {
		return ip.String()
	}
	return ""
}
