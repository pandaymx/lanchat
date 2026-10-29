package discover

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/brutella/dnssd"
)

// TestMetaToText 验证 TXT 字段齐全、布尔编码正确。
func TestMetaToText(t *testing.T) {
	m := ServerMeta{
		Version: "1.0.0",
		Path:    "/lctp",
		Name:    "MyServer",
		Auth:    "psk",
		ID:      "srv-1",
		TLS:     true,
	}
	text := m.toText()
	want := map[string]string{
		txtVer:  "1.0.0",
		txtPath: "/lctp",
		txtName: "MyServer",
		txtAuth: "psk",
		txtID:   "srv-1",
		txtTLS:  "1",
	}
	for k, v := range want {
		if text[k] != v {
			t.Errorf("TXT[%q] = %q，期望 %q", k, text[k], v)
		}
	}
	if len(text) != len(want) {
		t.Errorf("TXT 键数 = %d，期望 %d（不得放入密钥）", len(text), len(want))
	}

	// TLS 关闭时编码为 "0"。
	if got := (ServerMeta{}).toText()[txtTLS]; got != "0" {
		t.Errorf("默认 TLS = %q，期望 0", got)
	}
}

// TestShortInstance 验证实例名后缀的兜底与截断。
func TestShortInstance(t *testing.T) {
	cases := []struct {
		name string
		meta ServerMeta
		want string
	}{
		{"优先ID", ServerMeta{ID: "abcdef123456"}, "abcdef12"},
		{"ID含空格转横线", ServerMeta{ID: "a b c d e f"}, "a-b-c-d-"},
		{"ID空退化名称", ServerMeta{Name: " Office "}, "Office"},
		{"全空兜底", ServerMeta{}, "server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortInstance(tc.meta); got != tc.want {
				t.Fatalf("shortInstance = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// browseEntry 构造一条带 id TXT 的浏览结果。
func browseEntry(id, host string, port int, ips ...string) dnssd.BrowseEntry {
	text := map[string]string{}
	if id != "" {
		text[txtID] = id
	}
	ipObjs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipObjs = append(ipObjs, net.ParseIP(ip))
	}
	return dnssd.BrowseEntry{
		Host: host,
		Port: port,
		IPs:  ipObjs,
		Text: text,
	}
}

// TestUpsertDedupByHost 验证同一 Host 的多条 BrowseEntry（先无 TXT 的 probe、
// 后含 TXT 的通告）合并为一台、IP 聚合且去重，ID 从后续 TXT 补齐。
func TestUpsertDedupByHost(t *testing.T) {
	b := NewBrowser()
	// probe 包：无 TXT，只有 Host 与一个地址。
	b.upsert(browseEntry("", "host-a.local", 7700, "192.168.1.10"))
	// 同 Host 的通告：带 ID 与第二张网卡地址。
	b.upsert(browseEntry("srv-1", "host-a.local", 7700, "192.168.2.10"))
	// 重复上报同一 IP 不应产生重复条目。
	b.upsert(browseEntry("srv-1", "host-a.local", 7700, "192.168.1.10"))

	snap := b.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("服务器数 = %d，期望 1", len(snap))
	}
	srv := snap[0]
	if srv.ID != "srv-1" || srv.Port != 7700 {
		t.Fatalf("记录 = %+v，期望 id=srv-1 port=7700", srv)
	}
	if len(srv.Addresses) != 2 {
		t.Fatalf("地址数 = %d，期望 2: %v", len(srv.Addresses), srv.Addresses)
	}
}

// TestUpsertDistinctHost 验证不同 Host 保留为不同服务器。
func TestUpsertDistinctHost(t *testing.T) {
	b := NewBrowser()
	b.upsert(browseEntry("srv-1", "host-a.local", 7700, "10.0.0.1"))
	b.upsert(browseEntry("srv-2", "host-b.local", 7700, "10.0.0.2"))

	if got := len(b.Snapshot()); got != 2 {
		t.Fatalf("服务器数 = %d，期望 2", got)
	}
}

// TestUpsertHostStableWithoutTXT 验证缺失 TXT 时仍按 Host 稳定去重。
func TestUpsertHostStableWithoutTXT(t *testing.T) {
	b := NewBrowser()
	b.upsert(browseEntry("", "host-a.local", 7700, "10.0.0.1"))
	b.upsert(browseEntry("", "host-a.local", 7700, "10.0.0.1"))
	b.upsert(browseEntry("", "host-b.local", 7700, "10.0.0.2"))

	if got := len(b.Snapshot()); got != 2 {
		t.Fatalf("服务器数 = %d，期望 2（按 host 去重）", got)
	}
}

// TestRemovePrunesIPs 验证移除时按 IP 剔除，地址耗尽则整条删除。
func TestRemovePrunesIPs(t *testing.T) {
	b := NewBrowser()
	b.upsert(browseEntry("srv-1", "host-a.local", 7700, "192.168.1.10", "192.168.2.10"))

	b.remove(browseEntry("srv-1", "host-a.local", 7700, "192.168.1.10"))
	snap := b.Snapshot()
	if len(snap) != 1 || len(snap[0].Addresses) != 1 {
		t.Fatalf("剔除一地址后期望剩 1 台 1 地址，得到 %+v", snap)
	}
	if snap[0].Addresses[0].String() != "192.168.2.10" {
		t.Fatalf("剩余地址 = %s，期望 192.168.2.10", snap[0].Addresses[0])
	}

	b.remove(browseEntry("srv-1", "host-a.local", 7700, "192.168.2.10"))
	if got := len(b.Snapshot()); got != 0 {
		t.Fatalf("地址耗尽后期望删除整条，剩余 %d", got)
	}
}

// TestSnapshotSortedAndCopied 验证快照按 ID 排序且为深拷贝。
func TestSnapshotSortedAndCopied(t *testing.T) {
	b := NewBrowser()
	b.upsert(browseEntry("zzz", "h1.local", 1, "10.0.0.1"))
	b.upsert(browseEntry("aaa", "h2.local", 1, "10.0.0.2"))

	snap := b.Snapshot()
	if snap[0].ID != "aaa" || snap[1].ID != "zzz" {
		t.Fatalf("快照未按 ID 排序: %s %s", snap[0].ID, snap[1].ID)
	}
	// 修改快照不影响内部状态。
	snap[0].Addresses[0] = net.ParseIP("1.1.1.1")
	again := b.Snapshot()
	if again[0].Addresses[0].String() != "10.0.0.2" {
		t.Fatalf("快照非深拷贝，内部状态被污染: %v", again[0].Addresses)
	}
}

// TestIPLess 验证 IPv4 字节序比较。
func TestIPLess(t *testing.T) {
	if !ipLess(net.ParseIP("10.0.0.2"), net.ParseIP("10.0.0.10")) {
		t.Error("期望 10.0.0.2 < 10.0.0.10")
	}
	if ipLess(net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.1")) {
		t.Error("相等 IP 不应为 less")
	}
}

// TestUDPBeaconAdvertiseAndProbe 验证 UDP 广播/单播发现响应器在 mDNS 5353 被占用时仍能可靠发现节点。
func TestUDPBeaconAdvertiseAndProbe(t *testing.T) {
	adv, err := NewAdvertiser(17878, ServerMeta{
		Version: "1",
		Path:    "/lanchat",
		Name:    "BeaconNode",
		Auth:    "none",
		ID:      "beacon-srv-1",
	})
	if err != nil {
		t.Fatalf("NewAdvertiser failed: %v", err)
	}
	defer adv.Close()

	srvs := ProbeUDP(context.Background(), 180*time.Millisecond)
	var found bool
	for _, s := range srvs {
		if s.ID == "beacon-srv-1" && s.Port == 17878 && s.Name == "BeaconNode" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected beacon-srv-1 in ProbeUDP results, got %+v", srvs)
	}
}

// TestTailscalePeerDiscoveryAndDualStackPriority 验证 v4.7.0 Tailscale 节点 JSON 解析、Gossip 自动注入与 LAN+Tailscale 双栈优先级排序。
func TestTailscalePeerDiscoveryAndDualStackPriority(t *testing.T) {
	rawJSON := []byte(`{
		"BackendState": "Running",
		"TailscaleIPs": ["100.93.183.47", "fd7a:115c:a1e0::839:b730"],
		"Self": {
			"HostName": "jctx-dev",
			"TailscaleIPs": ["100.93.183.47"],
			"Online": true
		},
		"Peer": {
			"nodekey:1": {
				"HostName": "ymx-dev",
				"TailscaleIPs": ["100.83.15.53", "fd7a:115c:a1e0::1"],
				"Online": true
			},
			"nodekey:2": {
				"HostName": "tencent-cloud-test",
				"TailscaleIPs": ["100.105.48.87"],
				"Online": true
			},
			"nodekey:3": {
				"HostName": "archlinux",
				"TailscaleIPs": ["100.123.66.17"],
				"Online": false,
				"Active": false
			}
		}
	}`)

	ips := ParseTailscaleStatusJSON(rawJSON)
	if len(ips) != 2 || ips[0] != "100.105.48.87" || ips[1] != "100.83.15.53" {
		t.Fatalf("ParseTailscaleStatusJSON = %v, want [100.105.48.87 100.83.15.53]", ips)
	}

	tailscaleCacheMu.Lock()
	tailscaleStatusProvider = func() ([]byte, error) { return rawJSON, nil }
	tailscaleCacheExpiresAt = time.Time{}
	tailscaleCacheMu.Unlock()
	defer func() {
		tailscaleCacheMu.Lock()
		tailscaleStatusProvider = nil
		tailscaleCacheExpiresAt = time.Time{}
		tailscaleCacheMu.Unlock()
	}()

	book := NewGossipPeerBook(filepath.Join(t.TempDir(), "gossip_peers.json"))
	if added := book.SeedTailscalePeers(7878); added != 2 {
		t.Fatalf("SeedTailscalePeers added = %d, want 2", added)
	}
	eps := book.Endpoints()
	if len(eps) != 2 || eps[0] != "100.105.48.87:7878" || eps[1] != "100.83.15.53:7878" {
		t.Fatalf("GossipPeerBook.Endpoints() = %v", eps)
	}

	if !IsTailscaleIP("ws://100.105.48.87:7878/lanchat") || IsTailscaleIP("192.168.1.52:7878") {
		t.Fatal("IsTailscaleIP classification mismatch")
	}
	if LinkModeLabel("100.83.15.53:7878") != "🌐 Tailscale 直连" || LinkModeLabel("192.168.1.52:7878") != "⚡ 局域网直连" {
		t.Fatal("LinkModeLabel mismatch")
	}

	_, lanNet, _ := net.ParseCIDR("192.168.1.0/24")
	// 场景 A：同局域网双栈节点（192.168.1.88 + 100.83.15.53），同子网物理网卡 IP 排第一
	sameLAN := prioritizeDualStackWithSubnets([]net.IP{
		net.ParseIP("100.83.15.53"),
		net.ParseIP("192.168.1.88"),
	}, []*net.IPNet{lanNet})
	if len(sameLAN) != 2 || sameLAN[0].String() != "192.168.1.88" {
		t.Fatalf("expected physical LAN IP 192.168.1.88 first on same subnet, got %v", sameLAN)
	}

	// 场景 B：跨网段节点（对端内网 10.0.13.4 不在本机子网 + Tailscale 100.105.48.87），Tailscale IP 排第一
	crossNet := prioritizeDualStackWithSubnets([]net.IP{
		net.ParseIP("10.0.13.4"),
		net.ParseIP("100.105.48.87"),
	}, []*net.IPNet{lanNet})
	if len(crossNet) != 2 || crossNet[0].String() != "100.105.48.87" {
		t.Fatalf("expected Tailscale IP 100.105.48.87 first across subnets, got %v", crossNet)
	}
}
