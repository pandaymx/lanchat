package client

// e2e_test.go 验证 Client 层端到端加密闭环：
//   - 发送：会话成员全部在线且有公钥 → 正文加密（Body 空、Encrypted 非空）
//   - 接收：对端密文经事件总线 → 解密为明文
//   - 任一成员无公钥 → 退回明文（渐进）

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/e2e"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	"github.com/pandaymx/lanchat/pkg/transport/fake"
)

// keyringServer 是模拟 hub keyring API 的内存服务器。
func keyringServer(keys map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var req struct {
				DeviceID string `json:"device_id"`
				Pubkey   string `json:"pubkey"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			keys[req.DeviceID] = req.Pubkey
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			ids := r.URL.Query().Get("device_id")
			out := map[string]string{}
			for _, id := range splitComma(ids) {
				if p, ok := keys[id]; ok {
					out[id] = p
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": out})
		default:
			http.Error(w, "nope", http.StatusMethodNotAllowed)
		}
	}))
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// newTestClient 构造启用 E2E 的 client（fake conn + 内存 store + keyring）。
func newTestClient(t *testing.T, user, device string, id *e2e.Identity, krURL string, st *memory.MemoryStore) *Client {
	t.Helper()
	hello := protocol.Hello{UserID: user, DeviceID: device}
	cli := New(hello, fake.NewConn(device), st, event.New())
	cli.SetFileBase(krURL)
	if err := cli.SetE2E(id); err != nil {
		t.Fatalf("SetE2E: %v", err)
	}
	return cli
}

// TestE2EConversation：A→B 加密闭环。
func TestE2EConversation(t *testing.T) {
	ctx := context.Background()
	keys := map[string]string{}
	srv := keyringServer(keys)
	defer srv.Close()

	idA, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/a.bin")
	idB, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b.bin")
	// B 的公钥先注册（模拟 B 已自我声明/注册过）。
	keys["devB"] = base64.StdEncoding.EncodeToString(idB.PublicKey())

	// 会话 c1：成员 A、B。
	st := memory.New()
	_ = st.SaveConversation(ctx, protocol.Conversation{ID: "c1", Kind: "dm"})
	_ = st.SaveConversationMember(ctx, "c1", "userA")
	_ = st.SaveConversationMember(ctx, "c1", "userB")

	cliA := newTestClient(t, "userA", "devA", idA, srv.URL, st)
	// A 的公钥已被 SetE2E 注册；注入 B 在线。
	cliA.peers["devB"] = protocol.Presence{UserID: "userB", DeviceID: "devB", Online: true}

	// A 发消息 → 应加密。
	if err := cliA.SendMessage(ctx, "c1", "机密：攻击计划"); err != nil {
		t.Fatal(err)
	}
	sent, err := st.History(ctx, "c1", 0, 10)
	if err != nil || len(sent) != 1 {
		t.Fatalf("history = %d msgs, %v", len(sent), err)
	}
	m := sent[0]
	if m.Body != "" || m.Encrypted == "" {
		t.Fatalf("expected encrypted: body=%q enc=%q", m.Body, m.Encrypted[:min(20, len(m.Encrypted))])
	}
	// 密文里不应出现明文（hub 侧不可见）。
	env, err := e2e.Unmarshal(m.Encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Decrypt(idB.PrivateKey()); err != nil {
		t.Fatalf("B should decrypt: %v", err)
	}

	// B 视角：收到 A 的密文消息 → 事件总线应出现明文。
	stB := memory.New()
	cliB := newTestClient(t, "userB", "devB", idB, srv.URL, stB)
	sub := cliB.bus.Subscribe(16)
	defer sub.Close()
	// B 的 peers 里也有自己（含在会话成员里）。
	cliB.peers["devB"] = protocol.Presence{UserID: "userB", DeviceID: "devB", Online: true}

	// 直接走 deliver → publish 路径（模拟 hub FKDeliver）。
	m.ID = "srv-1" // hub 会重写 ID
	cliB.deliverMessage(context.Background(), &m)
	ev := <-sub.C()
	if ev.Kind != core.EventMessage || ev.Message.Body != "机密：攻击计划" {
		t.Fatalf("decrypted body = %q (kind=%v)", ev.Message.Body, ev.Kind)
	}
	// 事件载荷里不应再残留密文（Encrypted 已被解密清空）。
	if ev.Message.Encrypted != "" {
		t.Fatal("encrypted should be cleared after decrypt")
	}
	// B 的 History 路径也应解密（本地 store 落密文、返回前解密）。
	if _, err := stB.AppendMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	histB, _ := cliB.History(ctx, "c1", 0, 10)
	if len(histB) != 1 || histB[0].Body != "机密：攻击计划" {
		t.Fatalf("B History body = %q", histB[0].Body)
	}
}

// TestE2ELobbyEncryption：大厅（无成员表）= 全员广播，全部在线
// 设备（含自己）有公钥 → 加密；B/C 都能解。
func TestE2ELobbyEncryption(t *testing.T) {
	ctx := context.Background()
	keys := map[string]string{}
	srv := keyringServer(keys)
	defer srv.Close()

	idA, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/a.bin")
	idB, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b.bin")
	idC, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/c.bin")
	keys["devB"] = base64.StdEncoding.EncodeToString(idB.PublicKey())
	keys["devC"] = base64.StdEncoding.EncodeToString(idC.PublicKey())

	// 大厅会话：不建成员表。
	st := memory.New()
	cliA := newTestClient(t, "userA", "devA", idA, srv.URL, st)
	cliA.peers["devB"] = protocol.Presence{UserID: "userB", DeviceID: "devB", Online: true}
	cliA.peers["devC"] = protocol.Presence{UserID: "userC", DeviceID: "devC", Online: true}

	if err := cliA.SendMessage(ctx, "lobby", "大厅全员密文"); err != nil {
		t.Fatal(err)
	}
	hist, _ := st.History(ctx, "lobby", 0, 10)
	m := hist[0]
	if m.Body != "" || m.Encrypted == "" {
		t.Fatalf("lobby msg should be encrypted: body=%q", m.Body)
	}
	env, err := e2e.Unmarshal(m.Encrypted)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []*e2e.Identity{idA, idB, idC} {
		plain, err := env.Decrypt(id.PrivateKey())
		if err != nil {
			t.Fatalf("recipient should decrypt: %v", err)
		}
		if string(plain) != "大厅全员密文" {
			t.Fatalf("plain = %q", plain)
		}
	}
}

// TestE2EKeyPinning：首次见钉住公钥；换钥 → 发 EventE2EKeyChanged。
func TestE2EKeyPinning(t *testing.T) {
	ctx := context.Background()
	keys := map[string]string{}
	srv := keyringServer(keys)
	defer srv.Close()

	idA, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/a.bin")
	idB, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b.bin")
	idB2, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b2.bin")
	keys["devB"] = base64.StdEncoding.EncodeToString(idB.PublicKey())

	st := memory.New()
	bus := event.New()
	hello := protocol.Hello{UserID: "userA", DeviceID: "devA"}
	cli := New(hello, fake.NewConn("devA"), st, bus)
	cli.SetFileBase(srv.URL)
	if err := cli.SetE2E(idA); err != nil {
		t.Fatal(err)
	}
	sub := bus.Subscribe(8)
	defer sub.Close()
	cli.peers["devB"] = protocol.Presence{UserID: "userB", DeviceID: "devB", Online: true}

	// 第一次发：首次见 devB → pin 落库，无告警事件。
	if err := cli.SendMessage(ctx, "lobby", "hi1"); err != nil {
		t.Fatal(err)
	}
	if pin, _ := st.GetE2EPinnedKey(ctx, "devB"); pin == "" {
		t.Fatal("pin should be saved on first sight")
	}
	select {
	case ev := <-sub.C():
		t.Fatalf("first sight should not emit key-changed, got %v", ev.Kind)
	default:
	}

	// 换 B 的公钥（模拟换设备/中间人）。
	keys["devB"] = base64.StdEncoding.EncodeToString(idB2.PublicKey())
	if err := cli.SendMessage(ctx, "lobby", "hi2"); err != nil {
		t.Fatal(err)
	}
	got := false
	select {
	case ev := <-sub.C():
		if ev.Kind == core.EventE2EKeyChanged && ev.KeyChanged != nil &&
			ev.KeyChanged.DeviceID == "devB" && ev.KeyChanged.OldFingerprint != ev.KeyChanged.NewFingerprint {
			got = true
		}
	case <-ctx.Done():
	}
	if !got {
		t.Fatal("expected EventE2EKeyChanged on key rotation")
	}
	// pin 仍记录旧值（不自动更新——用户核对后手动重置）。
	if pin, _ := st.GetE2EPinnedKey(ctx, "devB"); pin == "" || pin == keys["devB"] {
		t.Fatalf("pin should keep old value, got %q", pin)
	}
}

// TestE2EKeyPinningRecv：中间人换钥后，换钥设备发过来的带新 E2EKey
// 的消息也应触发告警（不只发送方向查 keyring 才检测）。
func TestE2EKeyPinningRecv(t *testing.T) {
	ctx := context.Background()
	keys := map[string]string{}
	srv := keyringServer(keys)
	defer srv.Close()

	idA, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/a1.bin")
	idB, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b1.bin")
	idB2, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/b2.bin")
	pubB := base64.StdEncoding.EncodeToString(idB.PublicKey())
	pubB2 := base64.StdEncoding.EncodeToString(idB2.PublicKey())
	keys["devB"] = pubB

	st := memory.New()
	bus := event.New()
	cliB := New(protocol.Hello{UserID: "userB", DeviceID: "devB"},
		fake.NewConn("devB"), st, bus)
	cliB.SetFileBase(srv.URL)
	_ = cliB.SetE2E(idB)

	// A 发送方向查一次 keyring → pin 钉成旧公钥。
	cliB.peers["devA"] = protocol.Presence{UserID: "userA", DeviceID: "devA", Online: true}
	keys["devA"] = base64.StdEncoding.EncodeToString(idA.PublicKey())
	_ = st.SaveE2EPinnedKey(ctx, "devA", keys["devA"]) // 模拟已 pin devA
	_ = st.SaveE2EPinnedKey(ctx, "devB", pubB)         // devB 自己的 pin

	// 换钥后的 devB 发消息过来（自我声明新公钥 pubB2）。
	sub := bus.Subscribe(8)
	defer sub.Close()
	m := &protocol.StoredMessage{
		ID: "m1", ConversationID: "lobby", SenderDeviceID: "devB",
		E2EKey: pubB2, Body: "hi",
	}
	cliB.deliverMessage(ctx, m)
	got := false
	for ev := range sub.C() {
		if ev.Kind == core.EventE2EKeyChanged && ev.KeyChanged != nil &&
			ev.KeyChanged.DeviceID == "devB" && ev.KeyChanged.OldFingerprint != ev.KeyChanged.NewFingerprint {
			got = true
			break
		}
	}
	if !got {
		t.Fatal("expected key-changed event on inbound rotated E2EKey")
	}
}

// TestE2EPlaintextFallback：对方无公钥 → 明文（渐进）。
func TestE2EPlaintextFallback(t *testing.T) {
	ctx := context.Background()
	keys := map[string]string{}
	srv := keyringServer(keys)
	defer srv.Close()

	idA, _ := e2e.LoadOrCreateIdentity(t.TempDir() + "/a.bin")
	st := memory.New()
	_ = st.SaveConversation(ctx, protocol.Conversation{ID: "c1", Kind: "dm"})
	_ = st.SaveConversationMember(ctx, "c1", "userA")
	_ = st.SaveConversationMember(ctx, "c1", "userB")

	cliA := newTestClient(t, "userA", "devA", idA, srv.URL, st)
	cliA.peers["devB"] = protocol.Presence{UserID: "userB", DeviceID: "devB", Online: true}
	// B 未注册公钥（keys 为空）。

	if err := cliA.SendMessage(ctx, "c1", "明文消息"); err != nil {
		t.Fatal(err)
	}
	hist, _ := st.History(ctx, "c1", 0, 10)
	if hist[0].Body != "明文消息" || hist[0].Encrypted != "" {
		t.Fatalf("expected plaintext fallback: body=%q enc=%q", hist[0].Body, hist[0].Encrypted)
	}
	// 自我声明仍在（接收方 keyring 可收集）。
	if hist[0].E2EKey == "" {
		t.Fatal("E2EKey self-announce should always be present")
	}
}
