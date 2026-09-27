package discover

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempHome 把 HOME 指向临时目录并返回还原函数，用于隔离缓存文件。
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, had := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", dir); err != nil {
		t.Fatalf("设置 HOME: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("HOME", old)
		} else {
			_ = os.Unsetenv("HOME")
		}
	})
	return dir
}

// TestCachePath 验证缓存路径位于 HOME/.lanchat 下。
func TestCachePath(t *testing.T) {
	dir := withTempHome(t)
	p, err := CachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".lanchat", cacheFileName)
	if p != want {
		t.Fatalf("CachePath = %s，期望 %s", p, want)
	}
}

// TestCacheRoundTrip 验证缓存写入后读取往返一致。
func TestCacheRoundTrip(t *testing.T) {
	withTempHome(t)

	if c, err := LoadCache(); err != nil || c != nil {
		t.Fatalf("空缓存期望 (nil,nil)，得到 %v %v", c, err)
	}

	in := CachedServer{
		ID: "srv-1", Host: "192.168.1.10", Port: 7700,
		Path: "/lctp", TLS: true, SavedAt: 12345,
	}
	if err := SaveCache(in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || *out != in {
		t.Fatalf("往返不一致: %+v，期望 %+v", out, in)
	}

	// 权限应为 0600，目录 0700。
	p, _ := CachePath()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("缓存文件权限 = %o，期望 600", perm)
	}
}

// serverWith 构造一台带若干地址的候选服务器。
func serverWith(id string, ips ...string) Server {
	s := Server{ID: id, Port: 7700}
	for _, ip := range ips {
		s.Addresses = append(s.Addresses, net.ParseIP(ip))
	}
	return s
}

// TestRankDoesNotMutateInput 验证 Rank 返回新切片、不原地破坏输入。
func TestRankDoesNotMutateInput(t *testing.T) {
	in := []Server{
		serverWith("a", "10.0.0.1"),
		serverWith("b", "10.0.0.2"),
	}
	out, err := Rank(in, "")
	if err != nil {
		t.Fatal(err)
	}
	out[0].ID = "mutated"
	if in[0].ID != "a" {
		t.Fatal("Rank 修改了入参")
	}
}

// TestRankBadCIDR 验证非法 preferCIDR 返回 ErrBadCIDR。
func TestRankBadCIDR(t *testing.T) {
	if _, err := Rank(nil, "not-a-cidr"); !errors.Is(err, ErrBadCIDR) {
		t.Fatalf("错误 = %v，期望 ErrBadCIDR", err)
	}
}

// TestScoreOf 验证权重：命中 preferCIDR +2，命中本机 /24 +1。
func TestScoreOf(t *testing.T) {
	_, pref, _ := net.ParseCIDR("10.5.0.0/16")
	local := []*net.IPNet{mustCIDR("192.168.1.0/24")}

	// 同时命中偏好网段与本机网段。
	both := serverWith("a", "10.5.1.1", "192.168.1.20")
	if got := scoreOf(both, pref, local); got != 3 {
		t.Errorf("score = %d，期望 3（2+1）", got)
	}

	// 仅命中本机网段。
	localOnly := serverWith("b", "192.168.1.30")
	if got := scoreOf(localOnly, pref, local); got != 1 {
		t.Errorf("score = %d，期望 1", got)
	}

	// 均不命中。
	none := serverWith("c", "172.16.0.1")
	if got := scoreOf(none, pref, local); got != 0 {
		t.Errorf("score = %d，期望 0", got)
	}
}

// TestRankPreferCIDROrdering 验证 preferCIDR 命中者排前。
func TestRankPreferCIDROrdering(t *testing.T) {
	// 用本机不存在的网段，避免与 localIPv4Nets 叠加影响判定。
	in := []Server{
		serverWith("outside", "172.31.0.1"),
		serverWith("inside", "203.0.113.5"),
	}
	out, err := Rank(in, "203.0.113.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if out[0].ID != "inside" {
		t.Fatalf("排序首位 = %s，期望 inside", out[0].ID)
	}
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// TestBuildWSURL 验证 scheme、路径默认值与补全。
func TestBuildWSURL(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		path string
		tls  bool
		want string
	}{
		{"明文根路径", "127.0.0.1", 7700, "", false, "ws://127.0.0.1:7700/"},
		{"明文自定义路径", "127.0.0.1", 7700, "lctp", false, "ws://127.0.0.1:7700/lctp"},
		{"TLS", "127.0.0.1", 7700, "/lctp", true, "wss://127.0.0.1:7700/lctp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildWSURL(tc.host, tc.port, tc.path, tc.tls); got != tc.want {
				t.Fatalf("URL = %s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestFreshEnough 验证缓存有效期判断。
func TestFreshEnough(t *testing.T) {
	if !freshEnough(&CachedServer{SavedAt: 0}) {
		t.Error("SavedAt=0 视为可用")
	}
	now := time.Now().Unix()
	if !freshEnough(&CachedServer{SavedAt: now}) {
		t.Error("刚写入的缓存应视为新鲜")
	}
	if freshEnough(&CachedServer{SavedAt: now - int64(cacheValidWindow/time.Second) - 10}) {
		t.Error("超出有效窗口的缓存应视为过期")
	}
}
