package server

import (
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/protocol"
)

func TestNegotiateFeaturesEmpty(t *testing.T) {
	if got := negotiateFeatures(protocol.ClientCaps{Resume: true, Relay: true}); len(got) != 0 {
		t.Fatalf("M1 不应启用任何特性，得到 %v", got)
	}
}

func TestValidateOptions(t *testing.T) {
	good := Options{
		Listen: "127.0.0.1:0", Path: "/lctp", AuthMode: "none",
		HeartbeatInterval: time.Second, IdleTimeout: time.Second, ShutdownGrace: time.Second,
	}
	if err := validateOptions(good); err != nil {
		t.Fatalf("合法 Options 被拒: %v", err)
	}

	cases := []Options{
		func() Options { c := good; c.Listen = ""; return c }(),
		func() Options { c := good; c.Path = "lctp"; return c }(),
		func() Options { c := good; c.AuthMode = "bogus"; return c }(),
		func() Options { c := good; c.AuthMode = "psk"; return c }(),
		func() Options { c := good; c.IdleTimeout = 0; return c }(),
	}
	for i, bad := range cases {
		if err := validateOptions(bad); err == nil {
			t.Errorf("用例 %d 非法 Options 应报错", i)
		}
	}
}

func TestRegistryRevisionAndSnapshot(t *testing.T) {
	r := newRegistry()
	clients := make([]*Client, 3)
	for i := range clients {
		c := &Client{id: string(rune('a' + i)), nickname: "n", os: "test"}
		clients[i] = c
		rev := r.add(c)
		if rev != uint64(i+1) {
			t.Errorf("add 后 revision = %d, 期望 %d", rev, i+1)
		}
	}
	if r.len() != 3 {
		t.Fatalf("len = %d, 期望 3", r.len())
	}

	rev, users := r.snapshot()
	if rev != 3 || len(users) != 3 {
		t.Fatalf("snapshot = rev %d, %d users", rev, len(users))
	}
	users[0].Nickname = "MUTATED"
	_, users2 := r.snapshot()
	for _, u := range users2 {
		if u.Nickname == "MUTATED" {
			t.Fatal("snapshot 返回的切片应为拷贝，外部修改污染了内部状态")
		}
	}

	_, rev, ok := r.remove("a")
	if !ok || rev != 4 {
		t.Fatalf("remove: ok=%v rev=%d, 期望 true/4", ok, rev)
	}
	if _, _, ok := r.remove("a"); ok {
		t.Fatal("重复 remove 应返回 false")
	}

	// 同 ID replace 不应改变 revision。
	old := &Client{id: "b", nickname: "old"}
	r.replace(old)
	cur, _ := r.get("b")
	if cur != old {
		t.Fatal("replace 后应取到新 client")
	}
	if r.revision != 4 {
		t.Fatalf("replace 不应改变 revision，得到 %d", r.revision)
	}
}

func TestRegistryIdleClients(t *testing.T) {
	r := newRegistry()
	now := time.Now()
	active := &Client{id: "active"}
	active.lastSeen = now
	idle := &Client{id: "idle"}
	idle.lastSeen = now.Add(-time.Minute)
	r.add(active)
	r.add(idle)

	out := r.idleClients(now.Add(-30 * time.Second))
	if len(out) != 1 || out[0].id != "idle" {
		t.Fatalf("idleClients 返回 %v, 期望仅 idle", out)
	}
}

func TestAuthenticatorNone(t *testing.T) {
	a := newAuthenticator("none", "")
	if reason, ok := a.helloOK("10.0.0.1", ""); !ok || reason != "" {
		t.Fatalf("mode=none 应直接通过，得到 reason=%q ok=%v", reason, ok)
	}
}

func TestAuthenticatorPSKSuccess(t *testing.T) {
	hash, err := config.HashPSK("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	a := newAuthenticator("psk", hash)
	if _, ok := a.helloOK("10.0.0.1", "s3cret"); !ok {
		t.Fatal("正确口令应通过")
	}
}

func TestAuthenticatorPSKFailAndCooldown(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	clock := base
	hash, err := config.HashPSK("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	a := newAuthenticator("psk", hash)
	a.now = func() time.Time { return clock }

	for i := 0; i < maxAuthFails; i++ {
		if reason, ok := a.helloOK("10.0.0.1", "wrong"); ok || reason != "bad_psk" {
			t.Fatalf("第 %d 次失败应返回 bad_psk/false", i+1)
		}
	}
	// 第 6 次（>5）触发冷却。
	if reason, ok := a.helloOK("10.0.0.1", "s3cret"); ok || reason != "too_many_failures" {
		t.Fatalf("冷却期内即使口令正确也应拒绝，得到 reason=%q ok=%v", reason, ok)
	}

	// 冷却结束后可恢复。
	clock = base.Add(cooldown + time.Second)
	if _, ok := a.helloOK("10.0.0.1", "s3cret"); !ok {
		t.Fatal("冷却结束后正确口令应通过")
	}

	// 滑动窗口：旧计数在 1 分钟后应清零，不会立刻触发冷却。
	clock = base.Add(cooldown + time.Minute + time.Second)
	for i := 0; i < maxAuthFails; i++ {
		a.helloOK("10.0.0.2", "wrong")
	}
	if reason, ok := a.helloOK("10.0.0.2", "s3cret"); ok || reason != "too_many_failures" {
		t.Fatalf("同窗口第 6 次应触发冷却，得到 reason=%q ok=%v", reason, ok)
	}
}

func TestClientEnqueueAndTerminate(t *testing.T) {
	c := newClient(&websocket.Conn{}, "127.0.0.1")
	env, err := protocol.NewEnvelope("1", protocol.Heartbeat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.enqueue(env) {
		t.Fatal("空队列应能入队")
	}
	// terminate 后入队失败。
	// 不调用 c.terminate()，因为它会对零值 *websocket.Conn 调 Close 并 panic。
	c.closeO.Do(func() { close(c.closed) })
	if c.enqueue(env) {
		t.Fatal("closed 后入队应失败")
	}
}

func TestLengthConstants(t *testing.T) {
	if maxTextBytes != 4096 {
		t.Errorf("maxTextBytes = %d", maxTextBytes)
	}
	if maxStickerBytes != 256*1024 {
		t.Errorf("maxStickerBytes = %d", maxStickerBytes)
	}
	if !strings.HasPrefix(protocol.ProtocolVersion, "2") {
		t.Errorf("ProtocolVersion = %s", protocol.ProtocolVersion)
	}
}
