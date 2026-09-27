// Package discover 实现 LANChat 的服务发现：
//   - 服务端用 Advertiser 通过 mDNS 注册自身（非敏感元数据）；
//   - 客户端用 Browser 浏览并按实例 ID 去重，用 Resolve 做三级寻址。
//
// 本包只处理「发现 / 寻址」，不涉及鉴权与文件传输。mDNS 底层库为
// github.com/brutella/dnssd，经此薄封装隔离，便于将来整体替换。
package discover

import (
	"context"
	"strings"

	"github.com/brutella/dnssd"
)

// ServiceType 是 mDNS 服务类型；Domain 为默认域。
const (
	ServiceType = "_lanchat._tcp"
	Domain      = "local"
)

// TXT 记录的键名（方案 §8：绝不含密钥）。
const (
	txtVer  = "ver"
	txtPath = "path"
	txtName = "name"
	txtAuth = "auth"
	txtID   = "id"
	txtTLS  = "tls"
)

// ServerMeta 是注册到 mDNS 的非敏感元数据。
type ServerMeta struct {
	Version string // TXT "ver"  -> protocol.ProtocolVersion
	Path    string // TXT "path" -> WS 路径，如 /lctp
	Name    string // TXT "name" -> 服务器展示名
	Auth    string // TXT "auth" -> "psk" | "none"
	ID      string // TXT "id"   -> 服务器实例唯一 ID（去重依据）
	TLS     bool   // TXT "tls"  -> 是否启用 TLS
}

// toText 把元数据编码为 TXT 键值对。
func (m ServerMeta) toText() map[string]string {
	tls := "0"
	if m.TLS {
		tls = "1"
	}
	return map[string]string{
		txtVer:  m.Version,
		txtPath: m.Path,
		txtName: m.Name,
		txtAuth: m.Auth,
		txtID:   m.ID,
		txtTLS:  tls,
	}
}

// Advertiser 封装服务端 mDNS 注册。
type Advertiser struct {
	resp   dnssd.Responder
	handle dnssd.ServiceHandle
	cancel context.CancelFunc
}

// NewAdvertiser 在所有活动多播网卡上注册一个 _lanchat 服务实例。
// Close 时注销。
func NewAdvertiser(port int, meta ServerMeta) (*Advertiser, error) {
	instance := "lanchat-" + shortInstance(meta)
	// 仅在真实局域网网卡上注册，避免多网卡 probe 洪泛拖垮 responder；
	// 若一张 LAN 网卡都识别不到，留空让库回退到默认（全部多播网卡）。
	ifaces := lanInterfaces()
	svc, err := dnssd.NewService(dnssd.Config{
		Name:   instance,
		Type:   ServiceType,
		Domain: Domain,
		Port:   port,
		Text:   meta.toText(),
		Ifaces: ifaces,
	})
	if err != nil {
		return nil, err
	}

	resp, err := dnssd.NewResponder()
	if err != nil {
		return nil, err
	}
	handle, err := resp.Add(svc)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = resp.Respond(ctx) }()

	return &Advertiser{resp: resp, handle: handle, cancel: cancel}, nil
}

// Close 注销服务并停止响应（幂等由 context 与 Remove 语义保证）。
func (a *Advertiser) Close() {
	if a == nil {
		return
	}
	a.cancel()
	a.resp.Remove(a.handle)
}

// shortInstance 生成实例名后缀：优先用元数据 ID，其次名称，最后兜底 host。
// 仅保留 DNS 实例名友好的字符。
func shortInstance(meta ServerMeta) string {
	raw := meta.ID
	if raw == "" {
		raw = meta.Name
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "server"
	}
	// 取前 8 个无空格字符，避免实例名过长或含非法字符。
	raw = strings.ReplaceAll(raw, " ", "-")
	if len(raw) > 8 {
		raw = raw[:8]
	}
	return raw
}
