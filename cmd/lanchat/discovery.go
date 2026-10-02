package main

// mDNS 广告装配（方案 §8 / §18.4 场景 1）：信令服务器开始监听后，把自身的非敏感
// 元数据注册到 _lanchat._tcp，客户端（lanchat browse 与各端 UI 的发现列表）据此零配置发现。
//
// 这些开关有意不经 internal/config：discovery 参数只影响广告、不参与服务端配置校验，
// 留在 cmd 层可保持 config 契约稳定（AGENTS.md §2 责任田）。解析优先级与 config 一致：
// 显式 flag > 环境变量 LANCHAT_* > 默认值。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

// discovery 相关的环境变量名。
const (
	envDiscoveryEnabled = "LANCHAT_DISCOVERY_ENABLED"
	envServerName       = "LANCHAT_SERVER_NAME"
	envServerID         = "LANCHAT_SERVER_ID"
)

// discoverySettings 是 mDNS 广告所需的全部输入。
type discoverySettings struct {
	Enabled bool
	Name    string // TXT "name"，默认主机名
	ID      string // TXT "id"；为空则由 Name + 实际监听端口派生
	Path    string // TXT "path"
	Auth    string // TXT "auth"
}

// registerDiscoveryFlags 挂上 discovery 的三项 flag。值本身由 resolveDiscovery
// 解析（这里只是让 --help 可见，并让 fs.Visit 能识别用户显式给出的项）。
func registerDiscoveryFlags(fs *flag.FlagSet) {
	var enabled bool
	var name, id string
	fs.BoolVar(&enabled, "discover", true, "通过 mDNS 广告本服务（默认开启）")
	fs.StringVar(&name, "server-name", "", "mDNS 展示名，默认主机名")
	fs.StringVar(&id, "server-id", "", "mDNS 实例 ID，默认由展示名+端口派生")
}

// explicitFlag 返回用户显式给出的 flag 值。
func explicitFlag(fs *flag.FlagSet, name string) (string, bool) {
	var v string
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			v, found = f.Value.String(), true
		}
	})
	return v, found
}

// lookup 按 flag > env 取值；都未命中时返回 false，由调用方兜默认值。
func lookup(fs *flag.FlagSet, flagName, envKey string) (string, bool) {
	if v, ok := explicitFlag(fs, flagName); ok {
		return v, true
	}
	if v, ok := os.LookupEnv(envKey); ok {
		return v, true
	}
	return "", false
}

// resolveDiscovery 解析 mDNS 广告参数（path/auth 取自最终生效的服务端配置）。
func resolveDiscovery(fs *flag.FlagSet, path, authMode string) (discoverySettings, error) {
	d := discoverySettings{
		Enabled: true,
		Name:    defaultServerName(),
		Path:    path,
		Auth:    authMode,
	}
	if v, ok := lookup(fs, "discover", envDiscoveryEnabled); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			// flag 路径的非法布尔值由 flag 包在 Parse 阶段拦下，能走到这里只可能是环境变量。
			return d, fmt.Errorf("环境变量 %s 需要布尔值，得到 %q", envDiscoveryEnabled, v)
		}
		d.Enabled = b
	}
	if v, ok := lookup(fs, "server-name", envServerName); ok {
		if s := strings.TrimSpace(v); s != "" {
			d.Name = s
		}
	}
	if v, ok := lookup(fs, "server-id", envServerID); ok {
		d.ID = strings.TrimSpace(v)
	}
	return d, nil
}

// defaultServerName 用主机名作展示名，取不到时兜底 "lanchat"。
func defaultServerName() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "lanchat"
	}
	return h
}

// deriveServerID 由展示名与端口派生稳定实例 ID：同一主机名+端口重启后不变，
// 客户端可据此去重并命中 last_server 缓存（§8 三级寻址）。
func deriveServerID(name string, port int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", name, port)))
	return "srv-" + hex.EncodeToString(sum[:])[:12]
}

// portOf 从实际监听地址（如 192.168.1.47:19090、[::]:19090）取出端口。
func portOf(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("解析监听地址 %q: %w", addr, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("非法监听端口 %q", p)
	}
	return port, nil
}

// meta 生成 TXT 元数据。只含非敏感信息，绝不含口令或哈希（方案 §8）。
func (d discoverySettings) meta(port int) discover.ServerMeta {
	id := d.ID
	if id == "" {
		id = deriveServerID(d.Name, port)
	}
	return discover.ServerMeta{
		Version: protocol.ProtocolVersion,
		Path:    d.Path,
		Name:    d.Name,
		Auth:    d.Auth,
		ID:      id,
		TLS:     false, // 信令面当前不提供 wss/TLS，显式写死供客户端展示
	}
}

// ifaceDesc 描述本次广告将使用的网卡。browse 发现不到服务器时，这是第一手排障信息。
func ifaceDesc() string {
	if names := discover.LanInterfaceNames(); len(names) > 0 {
		return strings.Join(names, " ")
	}
	return "未识别到局域网网卡，回退全部多播网卡"
}

// startDiscovery 在信令端口就绪后注册 mDNS 广告，返回关停函数（等协程收尾并注销）。
// 必须在 srv.Serve 之前调用：它等待 Serve 关闭 Ready。
//
// 广告失败只记日志、不阻断启动：信令面本身仍可用，客户端退化为手动填地址。
// 这样容器/无多播网段的环境照样能起服务端，而 §18.4 场景 1 的失败原因会直接
// 打在启动日志里（含网卡与 TXT 字段）。
func startDiscovery(ctx context.Context, srv *server.Server, d discoverySettings, logf func(string, ...any)) func() {
	if !d.Enabled {
		logf("mDNS 广告已关闭（--discover=false 或 %s=0），客户端需手动填写服务器地址", envDiscoveryEnabled)
		return func() {}
	}

	var (
		adv  *discover.Advertiser
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		a, meta, port, err := advertiseWhenReady(ctx, srv, d)
		if err != nil {
			if ctx.Err() != nil {
				return // 关停竞态：尚未就绪就退出，不算失败
			}
			logf("mDNS 广告启动失败（信令服务继续运行，客户端需手动填写地址）：%v", err)
			return
		}
		adv = a
		logf("mDNS 广告已启动：%s 端口 %d 网卡[%s]",
			discover.ServiceType+"."+discover.Domain+".", port, ifaceDesc())
		logf("mDNS TXT: id=%s name=%s path=%s auth=%s ver=%s tls=%t",
			meta.ID, meta.Name, meta.Path, meta.Auth, meta.Version, meta.TLS)
	}()

	return func() {
		<-done
		if adv != nil {
			adv.Close()
			logf("mDNS 广告已注销")
		}
	}
}

// advertiseWhenReady 等信令端口真正开始监听后再注册 mDNS。
//
// 必须先监听：--listen 可写 :0（端口由内核分配），advertise 一个未就绪的地址
// 只会让客户端连上后失败；就绪后取 ListenAddr 的真实端口才能广告出可用记录。
func advertiseWhenReady(ctx context.Context, srv *server.Server,
	d discoverySettings,
) (*discover.Advertiser, discover.ServerMeta, int, error) {
	select {
	case <-srv.Ready():
	case <-ctx.Done():
		return nil, discover.ServerMeta{}, 0, ctx.Err()
	}
	port, err := portOf(srv.ListenAddr())
	if err != nil {
		return nil, discover.ServerMeta{}, 0, err
	}
	meta := d.meta(port)
	adv, err := discover.NewAdvertiser(port, meta)
	if err != nil {
		return nil, meta, port, err
	}
	return adv, meta, port, nil
}
