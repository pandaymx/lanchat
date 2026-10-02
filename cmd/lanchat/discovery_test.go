package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

// newServeFlagSet 构造与 serve 相同的 flag 集合并解析给定参数。
func newServeFlagSet(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	registerDiscoveryFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("解析 flag: %v", err)
	}
	return fs
}

// clearDiscoveryEnv 清掉 discovery 相关环境变量，并让 t.Setenv 在用例结束后复原。
func clearDiscoveryEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envDiscoveryEnabled, envServerName, envServerID} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("清理环境变量 %s: %v", k, err)
		}
	}
}

func TestResolveDiscoveryDefaults(t *testing.T) {
	clearDiscoveryEnv(t)

	d, err := resolveDiscovery(newServeFlagSet(t), "/lctp", "psk")
	if err != nil {
		t.Fatalf("resolveDiscovery: %v", err)
	}
	if !d.Enabled {
		t.Error("未给定时应默认开启 mDNS 广告")
	}
	if d.Name != defaultServerName() {
		t.Errorf("Name = %q，期望默认主机名 %q", d.Name, defaultServerName())
	}
	if d.ID != "" {
		t.Errorf("ID = %q，期望留空以便按端口派生", d.ID)
	}
	if d.Path != "/lctp" || d.Auth != "psk" {
		t.Errorf("Path/Auth = %q/%q，期望 /lctp/psk", d.Path, d.Auth)
	}
}

func TestResolveDiscoveryFlagBeatsEnv(t *testing.T) {
	clearDiscoveryEnv(t)
	t.Setenv(envDiscoveryEnabled, "true")
	t.Setenv(envServerName, "env-name")
	t.Setenv(envServerID, "env-id")

	fs := newServeFlagSet(t, "--discover=false", "--server-id=flag-id")
	d, err := resolveDiscovery(fs, "/lctp", "none")
	if err != nil {
		t.Fatalf("resolveDiscovery: %v", err)
	}
	if d.Enabled {
		t.Error("显式 --discover=false 应压过环境变量 true")
	}
	if d.Name != "env-name" {
		t.Errorf("Name = %q，期望未给 flag 时用环境变量 env-name", d.Name)
	}
	if d.ID != "flag-id" {
		t.Errorf("ID = %q，期望 flag 压过环境变量，得到 flag-id", d.ID)
	}
}

func TestResolveDiscoveryEnvApplies(t *testing.T) {
	clearDiscoveryEnv(t)
	t.Setenv(envDiscoveryEnabled, "0")
	t.Setenv(envServerName, "从环境")

	d, err := resolveDiscovery(newServeFlagSet(t), "/lctp", "psk")
	if err != nil {
		t.Fatalf("resolveDiscovery: %v", err)
	}
	if d.Enabled {
		t.Error("LANCHAT_DISCOVERY_ENABLED=0 应关闭广告")
	}
	if d.Name != "从环境" {
		t.Errorf("Name = %q，期望 从环境", d.Name)
	}
}

func TestResolveDiscoveryBlankNameKeepsDefault(t *testing.T) {
	clearDiscoveryEnv(t)

	fs := newServeFlagSet(t, "--server-name=   ")
	d, err := resolveDiscovery(fs, "/lctp", "psk")
	if err != nil {
		t.Fatalf("resolveDiscovery: %v", err)
	}
	if d.Name != defaultServerName() {
		t.Errorf("Name = %q，期望空白值退回默认主机名 %q", d.Name, defaultServerName())
	}
}

func TestResolveDiscoveryBadBool(t *testing.T) {
	clearDiscoveryEnv(t)
	t.Setenv(envDiscoveryEnabled, "maybe")

	if _, err := resolveDiscovery(newServeFlagSet(t), "/lctp", "psk"); err == nil ||
		!strings.Contains(err.Error(), "需要布尔值") {
		t.Fatalf("期望环境变量非法布尔值报错，得到 %v", err)
	}

	// flag 路径的非法布尔值由 flag 包在 Parse 阶段拒绝，走不到 resolveDiscovery。
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerDiscoveryFlags(fs)
	if err := fs.Parse([]string{"--discover=maybe"}); err == nil {
		t.Error("期望 flag 包拒绝 --discover=maybe")
	}
}

func TestDeriveServerID(t *testing.T) {
	a := deriveServerID("srv-a", 19090)
	if b := deriveServerID("srv-a", 19090); a != b {
		t.Errorf("同名同端口应稳定: %q != %q", a, b)
	}
	if c := deriveServerID("srv-a", 19091); a == c {
		t.Errorf("端口不同应得到不同 ID: %q", a)
	}
	if d := deriveServerID("srv-b", 19090); a == d {
		t.Errorf("名称不同应得到不同 ID: %q", a)
	}
	if !strings.HasPrefix(a, "srv-") || len(a) != 16 {
		t.Errorf("ID 格式异常: %q（期望 srv- + 12 位十六进制）", a)
	}
}

func TestPortOf(t *testing.T) {
	ok := map[string]int{
		"192.168.1.47:19090": 19090,
		"[::]:19090":         19090,
		"127.0.0.1:1":        1,
	}
	for addr, want := range ok {
		got, err := portOf(addr)
		if err != nil {
			t.Errorf("portOf(%q): %v", addr, err)
			continue
		}
		if got != want {
			t.Errorf("portOf(%q) = %d，期望 %d", addr, got, want)
		}
	}
	bad := []string{"", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:abc", "[::]:70000"}
	for _, addr := range bad {
		if got, err := portOf(addr); err == nil {
			t.Errorf("portOf(%q) = %d，期望报错", addr, got)
		}
	}
}

func TestDiscoveryMeta(t *testing.T) {
	d := discoverySettings{Name: "n1", Path: "/lctp", Auth: "psk"}

	meta := d.meta(19090)
	if meta.ID != deriveServerID("n1", 19090) {
		t.Errorf("ID = %q，期望由名称+端口派生", meta.ID)
	}
	if meta.Version != protocol.ProtocolVersion {
		t.Errorf("Version = %q，期望 %q", meta.Version, protocol.ProtocolVersion)
	}
	if meta.Path != "/lctp" || meta.Name != "n1" || meta.Auth != "psk" {
		t.Errorf("TXT 字段透传异常: %+v", meta)
	}
	if meta.TLS {
		t.Error("当前无 TLS，TXT tls 必须为 false")
	}

	d.ID = "fixed-id"
	if got := d.meta(19090).ID; got != "fixed-id" {
		t.Errorf("显式 ID 被覆盖: %q", got)
	}
}

// newTestServer 构造未开始监听的信令服务器（Ready 永不关闭，用于关停路径）。
func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	srv, err := server.New(server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Second,
		ShutdownGrace:     time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func TestAdvertiseWhenReadyStopsOnCanceledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := advertiseWhenReady(ctx, newTestServer(t), discoverySettings{Name: "n"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled，得到 %v", err)
	}
}

func TestStartDiscoveryDisabled(t *testing.T) {
	var lines []string
	shutdown := startDiscovery(context.Background(), newTestServer(t), discoverySettings{Enabled: false},
		func(s string, _ ...any) { lines = append(lines, s) })
	shutdown()

	if len(lines) != 1 || !strings.Contains(lines[0], "mDNS 广告已关闭") {
		t.Fatalf("期望输出关闭提示，得到 %v", lines)
	}
}

// TestStartDiscoveryShutsDownWhenNeverReady 覆盖生命周期死锁：服务器从未就绪
// 且 ctx 已取消时，关停函数必须立即返回（否则 serve 退出路径会挂住）。
func TestStartDiscoveryShutsDownWhenNeverReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	shutdown := startDiscovery(ctx, newTestServer(t),
		discoverySettings{Enabled: true, Name: "n", Path: "/lctp", Auth: "none"},
		func(string, ...any) {})
	cancel()

	done := make(chan struct{})
	go func() {
		shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("关停 mDNS 广告时挂死")
	}
}

// TestRunServeExitsWhenPortBusy 覆盖退出路径：监听端口被占时 Serve 在 Ready() 之前
// 就返回错误，广告协程仍停在等待就绪上，runServe 必须先取消 ctx 再收尾，否则会挂死。
func TestRunServeExitsWhenPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占位监听失败: %v", err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() {
		done <- runServe([]string{
			"--listen", ln.Addr().String(),
			"--auth-mode", "none",
			"--relay-enabled=false",
			"--server-name", "unit-test",
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("端口被占时期望返回错误")
		}
		if !strings.Contains(err.Error(), "监听") {
			t.Errorf("错误信息应指向监听失败，得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServe 在监听失败后挂死（mDNS 广告协程未被取消）")
	}
}
