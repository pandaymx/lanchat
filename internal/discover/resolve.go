package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 默认寻址参数（方案 §8）。
const (
	DefaultBrowseTimeout = 3 * time.Second
	DefaultProbeTimeout  = 1 * time.Second
	cacheFileName        = "last_server.json"
	cacheValidWindow     = 7 * 24 * time.Hour // 缓存条目自身有效期
)

// 寻址相关错误。
var (
	ErrNoServer   = errors.New("discover: no reachable server")
	ErrBadAddress = errors.New("discover: invalid explicit address")
	ErrBadCIDR    = errors.New("discover: invalid prefer cidr")
)

// ResolveResult 是寻址结果，可直接拼 WS URL 与候选地址。
type ResolveResult struct {
	ID     string
	WSURL  string
	Server Server
}

// CachedServer 是 last_server.json 的结构（仅地址类信息，不含密钥）。
type CachedServer struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
	TLS     bool   `json:"tls"`
	SavedAt int64  `json:"savedAt"`
}

// CachePath 返回平台缓存文件路径（~/.lanchat/last_server.json）。
func CachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lanchat", cacheFileName), nil
}

// LoadCache 读取缓存；文件不存在时返回 (nil, nil)。
func LoadCache() (*CachedServer, error) {
	p, err := CachePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c CachedServer
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCache 写入缓存（自动创建目录）。
func SaveCache(c CachedServer) error {
	p, err := CachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Rank 按「同网段优先」对候选服务器排序：
//   - 与本机任一非环回 IPv4 接口共享 /24 的地址所在服务器排前；
//   - preferCIDR 非空时，落在该网段内的地址再提一级。
//
// 纯函数：不修改入参，返回新切片。
func Rank(servers []Server, preferCIDR string) ([]Server, error) {
	var pref *net.IPNet
	if preferCIDR != "" {
		_, network, err := net.ParseCIDR(preferCIDR)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrBadCIDR, preferCIDR)
		}
		pref = network
	}

	localNets := localIPv4Nets()

	out := append([]Server(nil), servers...)
	sort.SliceStable(out, func(i, j int) bool {
		return scoreOf(out[i], pref, localNets) > scoreOf(out[j], pref, localNets)
	})
	return out, nil
}

// scoreOf 越大越优先：命中 preferCIDR +2，命中本机 /24 +1。
func scoreOf(s Server, prefer *net.IPNet, localNets []*net.IPNet) int {
	score := 0
	for _, ip := range s.Addresses {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		if prefer != nil && prefer.Contains(v4) {
			score += 2
		}
		for _, n := range localNets {
			if n.Contains(v4) {
				score += 1
				break
			}
		}
	}
	return score
}

// localIPv4Nets 返回本机所有非环回 IPv4 接口所在的 /24 网络。
func localIPv4Nets() []*net.IPNet {
	var nets []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
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
			// 强制按 /24 比较。
			mask := net.CIDRMask(24, 32)
			nets = append(nets, &net.IPNet{IP: v4.Mask(mask), Mask: mask})
		}
	}
	return nets
}

// Resolve 执行三级寻址（方案 §8）：
//  1. last_server.json 缓存（在 probeTimeout 内可连即用）
//  2. explicit 显式地址（非空时直接用）
//  3. mDNS 浏览 browseTimeout，去重 + Rank 后取首个可连者
//
// 成功时异步刷新缓存（仅地址信息）。
func Resolve(ctx context.Context, explicit, preferCIDR string,
	browseTimeout, probeTimeout time.Duration) (*ResolveResult, error) {
	if probeTimeout <= 0 {
		probeTimeout = DefaultProbeTimeout
	}
	if browseTimeout <= 0 {
		browseTimeout = DefaultBrowseTimeout
	}

	// 第 2 级：显式地址优先于 mDNS，但排在缓存之后（缓存命中可零等待）。
	if explicit != "" {
		return resolveExplicit(ctx, explicit, preferCIDR, probeTimeout)
	}

	// 第 1 级：缓存。
	if c, err := LoadCache(); err == nil && c != nil && freshEnough(c) {
		hostport := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		if reachable(ctx, hostport, probeTimeout) {
			return &ResolveResult{
				ID:    c.ID,
				WSURL: buildWSURL(c.Host, c.Port, c.Path, c.TLS),
				Server: Server{
					ID: c.ID, Path: c.Path, Port: c.Port, TLS: c.TLS,
					Addresses: resolveHostIPs(c.Host),
				},
			}, nil
		}
	}

	// 第 3 级：mDNS 浏览。
	browseCtx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()

	browser := NewBrowser()
	got := make(chan struct{}, 1)
	go func() {
		_ = browser.Browse(browseCtx, func([]Server) {
			select {
			case got <- struct{}{}:
			default:
			}
		})
	}()

	// 每当有新结果即尝试排序后的候选；窗口结束前命中即可返回。
	deadline := time.NewTimer(browseTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-got:
			if r := tryCandidates(ctx, browser, preferCIDR, probeTimeout); r != nil {
				saveResultCache(r)
				return r, nil
			}
		case <-deadline.C:
			if r := tryCandidates(ctx, browser, preferCIDR, probeTimeout); r != nil {
				saveResultCache(r)
				return r, nil
			}
			return nil, ErrNoServer
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// resolveExplicit 解析并探测显式地址，形式为 host:port，可附路径查询。
func resolveExplicit(ctx context.Context, explicit, preferCIDR string,
	probeTimeout time.Duration) (*ResolveResult, error) {
	host, portStr, err := net.SplitHostPort(explicit)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadAddress, explicit)
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("%w: bad port", ErrBadAddress)
	}
	hostport := net.JoinHostPort(host, portStr)
	if !reachable(ctx, hostport, probeTimeout) {
		return nil, fmt.Errorf("%w: %s", ErrNoServer, explicit)
	}
	return &ResolveResult{
		WSURL:  buildWSURL(host, port, "", false),
		Server: Server{Addresses: resolveHostIPs(host), Port: port},
	}, nil
}

// tryCandidates 对当前浏览快照排序后逐个探测，返回首个可达者。
func tryCandidates(ctx context.Context, b *Browser, preferCIDR string,
	probeTimeout time.Duration) *ResolveResult {
	ranked, err := Rank(b.Snapshot(), preferCIDR)
	if err != nil {
		return nil
	}
	for _, s := range ranked {
		for _, ip := range s.Addresses {
			host := ip.String()
			hostport := net.JoinHostPort(host, strconv.Itoa(s.Port))
			if reachable(ctx, hostport, probeTimeout) {
				return &ResolveResult{
					ID:     s.ID,
					WSURL:  buildWSURL(host, s.Port, s.Path, s.TLS),
					Server: s,
				}
			}
		}
	}
	return nil
}

// reachable 对 host:port 做带超时的 TCP 拨号探测（只验可达）。
func reachable(ctx context.Context, hostport string, timeout time.Duration) bool {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(probeCtx, "tcp", hostport)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// buildWSURL 拼装 WS 地址；path 为空时用根路径。
func buildWSURL(host string, port int, path string, tls bool) string {
	scheme := "ws"
	if tls {
		scheme = "wss"
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, strconv.Itoa(port)), path)
}

// resolveHostIPs 尽力把 host 解析为 IPv4 列表；IP 字面量直接返回。
func resolveHostIPs(host string) []net.IP {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []net.IP{v4}
		}
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4)
		}
	}
	return out
}

// freshEnough 校验缓存未过自身有效期。
func freshEnough(c *CachedServer) bool {
	if c.SavedAt == 0 {
		return true
	}
	return time.Since(time.Unix(c.SavedAt, 0)) < cacheValidWindow
}

// saveResultCache 尽力写缓存，失败不影响寻址结果。
func saveResultCache(r *ResolveResult) {
	var host string
	if len(r.Server.Addresses) > 0 {
		host = r.Server.Addresses[0].String()
	}
	c := CachedServer{
		ID:      r.ID,
		Host:    host,
		Port:    r.Server.Port,
		Path:    r.Server.Path,
		TLS:     r.Server.TLS,
		SavedAt: time.Now().Unix(),
	}
	_ = SaveCache(c)
}
