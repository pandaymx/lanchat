package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/protocol"
)

func TestNegotiateFeatures(t *testing.T) {
	// 中继未启用：只有 resume，客户端未声明 resume 时为空。
	if got := negotiateFeatures(protocol.ClientCaps{}, false); len(got) != 0 {
		t.Fatalf("无匹配能力时应为空，得到 %v", got)
	}
	// resume 恒可用。
	if got := negotiateFeatures(protocol.ClientCaps{Resume: true}, false); len(got) != 1 || got[0] != "resume" {
		t.Fatalf("应协商出 resume，得到 %v", got)
	}
	// 中继启用且客户端声明 relay：应得到 resume + relay。
	got := negotiateFeatures(protocol.ClientCaps{Resume: true, Relay: true}, true)
	if len(got) != 2 {
		t.Fatalf("应协商出 resume+relay，得到 %v", got)
	}
	// 中继未启用时，即使客户端声明 relay 也不应返回。
	if got := negotiateFeatures(protocol.ClientCaps{Relay: true}, false); len(got) != 0 {
		t.Fatalf("中继未启用不应返回 relay，得到 %v", got)
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

func TestRouteMsgReadAndRevoke(t *testing.T) {
	h := newHub(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
	}, nil)
	c1 := newClient(&websocket.Conn{}, "127.0.0.1")
	c1.id = "u1"
	c2 := newClient(&websocket.Conn{}, "127.0.0.1")
	c2.id = "u2"
	h.reg.add(c1)
	h.reg.add(c2)

	readEnv, err := protocol.NewEnvelope("r1", protocol.MsgRead, protocol.MsgReadPayload{MsgID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	readEnv.To = "u2"
	h.route(c1, readEnv)

	got := <-c2.send
	if got.Type != protocol.MsgRead || got.From != "u1" {
		t.Fatalf("expected MSG_READ from u1, got %+v", got)
	}
	ack := <-c1.send
	if ack.Type != protocol.MsgAck {
		t.Fatalf("expected MSG_ACK, got %+v", ack)
	}

	revEnv, err := protocol.NewEnvelope("rv1", protocol.MsgRevoke, protocol.MsgRevokePayload{MsgID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	revEnv.Group = protocol.GroupBroadcast
	h.route(c1, revEnv)

	gotRev := <-c2.send
	if gotRev.Type != protocol.MsgRevoke || gotRev.From != "u1" || gotRev.Group != protocol.GroupBroadcast {
		t.Fatalf("expected broadcast MSG_REVOKE from u1, got %+v", gotRev)
	}
	<-c1.send

	// 空 msgID 应返回 invalid_msg_id 错误。
	badEnv, _ := protocol.NewEnvelope("bad", protocol.MsgRevoke, protocol.MsgRevokePayload{MsgID: "   "})
	badEnv.To = "u2"
	h.route(c1, badEnv)
	errEnv := <-c1.send
	if errEnv.Type != protocol.Error {
		t.Fatalf("expected ERROR for empty msgID, got %+v", errEnv)
	}
}

func TestStatusAndHealthEndpoints(t *testing.T) {
	srv, err := New(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	<-srv.Ready()

	baseURL := "http://" + srv.ListenAddr()

	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d", resp.StatusCode)
	}

	resp, err = http.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/status status = %d", resp.StatusCode)
	}
	var snap StatusSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.AuthMode != "none" || snap.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	pageResp, err := http.Get(baseURL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("/status status = %d", pageResp.StatusCode)
	}
}

func TestSpoolOfflineStoreAndChannelHistory(t *testing.T) {
	h := newHub(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
	}, nil)
	c1 := newClient(&websocket.Conn{}, "127.0.0.1")
	c1.id = "dev-sender"
	c1.nickname = "Sender"
	h.reg.add(c1)

	// 发送给离线目标 u-offline，应暂存并返回 status="queued"。
	env, err := protocol.NewEnvelope("m-offline-1", protocol.TextMsg, protocol.TextPayload{Text: "offline hello"})
	if err != nil {
		t.Fatal(err)
	}
	env.To = "dev-offline"
	h.route(c1, env)

	ack := <-c1.send
	if ack.Type != protocol.MsgAck {
		t.Fatalf("expected MSG_ACK, got %+v", ack)
	}
	var ackBody protocol.MsgAckPayload
	if err := ack.DecodePayload(&ackBody); err != nil {
		t.Fatal(err)
	}
	if ackBody.Status != "queued" {
		t.Fatalf("expected queued status, got %q", ackBody.Status)
	}

	// 当 dev-offline 上线时，drainUser 应按序吐出暂存消息。
	offline := h.spool.drainUser("dev-offline", time.Now())
	if len(offline) != 1 || offline[0].ID != "m-offline-1" {
		t.Fatalf("expected 1 offline message replayed, got %+v", offline)
	}

	// 频道历史缓冲与回放验证。
	chMsg, err := protocol.NewEnvelope("ch-msg-1", protocol.TextMsg, protocol.TextPayload{Text: "channel hello"})
	if err != nil {
		t.Fatal(err)
	}
	chMsg.From = "dev-sender"
	chMsg.Group = "ch-1"
	now := time.Now()
	h.spool.appendChannel("ch-1", chMsg, now)
	hist := h.spool.channelHistory("ch-1", now)
	if len(hist) != 1 || hist[0].ID != "ch-msg-1" {
		t.Fatalf("expected 1 channel history message, got %+v", hist)
	}

	// M18: 验证 StateFile 原子落盘与服务端重启后恢复离线队列及频道列表。
	stateFile := filepath.Join(t.TempDir(), "server-state.json")
	hPersist := newHub(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
		StateFile:         stateFile,
	}, nil)
	chID, err := hPersist.channels.Create("架构讨论组", "dev-sender", "M18 持久化", false)
	if err != nil {
		t.Fatal(err)
	}
	hPersist.spool.enqueueUser("dev-offline-2", env, now)
	hPersist.spool.appendChannel(chID, chMsg, now)

	hRestored := newHub(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
		StateFile:         stateFile,
	}, nil)
	if len(hRestored.channels.List()) != 1 || hRestored.channels.List()[0].ID != chID {
		t.Fatalf("expected restored channel %s, got %+v", chID, hRestored.channels.List())
	}
	if len(hRestored.spool.drainUser("dev-offline-2", now)) != 1 {
		t.Fatal("expected restored offline message for dev-offline-2")
	}
	if len(hRestored.spool.channelHistory(chID, now)) != 1 {
		t.Fatal("expected restored channel history for chID")
	}

	// M25: 验证 /metrics Prometheus 端点与路由计数指标。
	srv, err := New(Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
		StateFile:         stateFile,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	<-srv.Ready()

	resp, err := http.Get("http://" + srv.ListenAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /metrics, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "lanchat_active_channels 1") {
		t.Fatalf("unexpected /metrics output:\n%s", string(body))
	}
}
