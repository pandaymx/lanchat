package discover

import (
	"context"
	"net"
	"sort"
	"sync"

	"github.com/brutella/dnssd"
)

// Server 是去重后的一条服务器记录（同一实例的多张网卡地址合并）。
type Server struct {
	ID        string
	Name      string
	Path      string
	Auth      string
	TLS       bool
	Version   string
	Addresses []net.IP // 该实例所有网卡 IPv4 地址
	Port      int
}

// Browser 持续浏览 mDNS，维护按实例 ID 去重后的服务器集合。
//
// 并发模型：dnssd 在自身 goroutine 内回调 add/rmv；内部 map 由 mu 保护，
// Snapshot 与浏览回调可安全并发。
type Browser struct {
	mu      sync.Mutex
	entries map[string]*Server // key = Host（见 keyOf）
}

// NewBrowser 创建一个空的浏览器。
func NewBrowser() *Browser {
	return &Browser{entries: map[string]*Server{}}
}

// Browse 在 ctx 周期内常驻浏览，结果变更时回调当前去重后的快照（已排序）。
// ctx 取消后浏览结束。
func (b *Browser) Browse(ctx context.Context, onUpdate func([]Server)) error {
	add := func(e dnssd.BrowseEntry) {
		b.mu.Lock()
		b.upsert(e)
		snap := b.lockedSnapshot()
		b.mu.Unlock()
		if onUpdate != nil {
			onUpdate(snap)
		}
	}
	rmv := func(e dnssd.BrowseEntry) {
		b.mu.Lock()
		b.remove(e)
		snap := b.lockedSnapshot()
		b.mu.Unlock()
		if onUpdate != nil {
			onUpdate(snap)
		}
	}
	return dnssd.LookupType(ctx, ServiceType+"."+Domain+".", add, rmv)
}

// Snapshot 返回当前去重后服务器集合的排序拷贝。
func (b *Browser) Snapshot() []Server {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lockedSnapshot()
}

// keyOf 返回去重键。固定使用 Host：同一实例的主机名在 probe 包（无 TXT）
// 与后续通告（含 TXT）中都稳定存在，多网卡地址也据此天然聚合。
// 不能用 TXT.id 作键——首次学到的 probe 包不含 TXT，会先用 Host 建条目，
// 通告到达后再按 ID 另建一条，导致同一实例分裂成两条记录。
func keyOf(e dnssd.BrowseEntry) string {
	return e.Host
}

// upsert 合并一条浏览结果：同键实例的多网卡 IP 聚合。
func (b *Browser) upsert(e dnssd.BrowseEntry) {
	key := keyOf(e)
	srv, ok := b.entries[key]
	if !ok {
		srv = &Server{}
		b.entries[key] = srv
	}
	if id := e.Text[txtID]; id != "" {
		srv.ID = id
	}
	srv.Name = e.Text[txtName]
	srv.Path = e.Text[txtPath]
	srv.Auth = e.Text[txtAuth]
	srv.Version = e.Text[txtVer]
	srv.TLS = e.Text[txtTLS] == "1"
	srv.Port = e.Port
	for _, ip := range e.IPs {
		if v4 := ip.To4(); v4 != nil && !containsIP(srv.Addresses, v4) {
			srv.Addresses = append(srv.Addresses, v4)
		}
	}
}

// remove 删除一条浏览结果：按其 IP 从合并记录中剔除；记录无剩余地址时整条移除。
func (b *Browser) remove(e dnssd.BrowseEntry) {
	key := keyOf(e)
	srv, ok := b.entries[key]
	if !ok {
		return
	}
	for _, ip := range e.IPs {
		if v4 := ip.To4(); v4 != nil {
			srv.Addresses = removeIP(srv.Addresses, v4)
		}
	}
	if len(srv.Addresses) == 0 {
		delete(b.entries, key)
	}
}

// lockedSnapshot 调用方持锁时返回排序后的拷贝。
func (b *Browser) lockedSnapshot() []Server {
	out := make([]Server, 0, len(b.entries))
	for _, s := range b.entries {
		cp := *s
		cp.Addresses = append([]net.IP(nil), s.Addresses...)
		sortIPs(cp.Addresses)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, x := range ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}

func removeIP(ips []net.IP, ip net.IP) []net.IP {
	out := ips[:0]
	for _, x := range ips {
		if !x.Equal(ip) {
			out = append(out, x)
		}
	}
	return out
}

func sortIPs(ips []net.IP) {
	sort.Slice(ips, func(i, j int) bool {
		return ipLess(ips[i], ips[j])
	})
}

// ipLess 按 IPv4 字节序比较，保证输出稳定。
func ipLess(a, b net.IP) bool {
	aa, ba := a.To4(), b.To4()
	if aa != nil && ba != nil {
		for i := 0; i < 4; i++ {
			if aa[i] != ba[i] {
				return aa[i] < ba[i]
			}
		}
		return false
	}
	return a.String() < b.String()
}
