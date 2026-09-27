package discover

import (
	"net"
	"testing"

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
