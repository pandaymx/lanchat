package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// ---- 测试替身 ---------------------------------------------------------------

// stubDialer 是 Dialer 的测试替身：每次拨号 new 一个 stubClient 并计数。
type stubDialer struct {
	mu      sync.Mutex
	count   int
	clients []*stubClient
	err     error
}

func (d *stubDialer) dial(_ context.Context, _ DialOptions) (Client, core.Store, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.count++
	if d.err != nil {
		return nil, nil, d.err
	}
	cli := newStubClient()
	d.clients = append(d.clients, cli)
	return cli, memory.New(), nil
}

func (d *stubDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.count
}

func (d *stubDialer) client(i int) *stubClient {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clients[i]
}

// newTestManager 造一个 janitor 已关掉（SweepInterval=1h）的 Manager，
// 清扫走手动 SweepExpired，避免测试依赖时序。
func newTestManager(t *testing.T, d *stubDialer) *Manager {
	t.Helper()
	if d == nil {
		d = &stubDialer{}
	}
	m := NewManager(ManagerConfig{
		User:          "alice",
		ConvID:        "lobby",
		DialTimeout:   time.Second,
		SessionTTL:    time.Minute,
		SweepInterval: time.Hour,
	}, d.dial)
	t.Cleanup(m.CloseAll)
	return m
}

// ---- cookies ----------------------------------------------------------------

// TestNewSessionID_UniqueAndLength 验证会话 ID 是 16 位 hex 且次次不同。
func TestNewSessionID_UniqueAndLength(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		id := newSessionID()
		if len(id) != 16 {
			t.Fatalf("session id len = %d, want 16 (8 bytes hex)", len(id))
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("session id %q contains non-hex char %q", id, c)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate session id %q", id)
		}
		seen[id] = struct{}{}
	}
}

// TestCookie_IssueAndRead 验证签发的 cookie 能被同请求读回，且属性正确。
func TestCookie_IssueAndRead(t *testing.T) {
	id := newSessionID()
	rec := httptest.NewRecorder()
	issueSessionCookie(rec, id)

	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("cookie %q not set", SessionCookieName)
	}
	if cookie.Value != id {
		t.Errorf("cookie value = %q, want %q", cookie.Value, id)
	}
	if !cookie.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}

	// 读回：把 cookie 塞进请求。
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	if got := readSessionID(req); got != id {
		t.Errorf("readSessionID = %q, want %q", got, id)
	}
}

// TestReadSessionID_Missing 验证无 cookie 时返回空串而非报错。
func TestReadSessionID_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := readSessionID(req); got != "" {
		t.Errorf("readSessionID = %q, want empty", got)
	}
}

// ---- Manager.GetOrCreate -----------------------------------------------------

// TestManager_GetOrCreate_DialsOncePerCookie 验证同一 cookie 多次取只拨号一次。
func TestManager_GetOrCreate_DialsOncePerCookie(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	s1, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("first GetOrCreate: %v", err)
	}
	s2, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("second GetOrCreate: %v", err)
	}
	if s1 != s2 {
		t.Error("same cookie returned different sessions")
	}
	if d.dialCount() != 1 {
		t.Errorf("dial count = %d, want 1", d.dialCount())
	}
}

// TestManager_GetOrCreate_DialsPerCookie 验证不同 cookie 各自拨号。
func TestManager_GetOrCreate_DialsPerCookie(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	s1, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("GetOrCreate a: %v", err)
	}
	s2, err := m.GetOrCreate(t.Context(), "cookie-b")
	if err != nil {
		t.Fatalf("GetOrCreate b: %v", err)
	}
	if s1 == s2 {
		t.Error("different cookies returned same session")
	}
	if d.dialCount() != 2 {
		t.Errorf("dial count = %d, want 2", d.dialCount())
	}
}

// TestManager_GetOrCreate_RebuildsDeadSession 验证 hub 断开（cli.Done 关闭）后
// 同一 cookie 再取会重建 Session 而不是返回死会话。
func TestManager_GetOrCreate_RebuildsDeadSession(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	s1, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("first GetOrCreate: %v", err)
	}
	close(d.client(0).done) // 模拟 hub 连接断开

	s2, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("rebuild GetOrCreate: %v", err)
	}
	if s1 == s2 {
		t.Error("dead session was not rebuilt")
	}
	if d.dialCount() != 2 {
		t.Errorf("dial count = %d, want 2", d.dialCount())
	}
	if !d.client(0).closed() {
		t.Error("dead session's client was not closed on rebuild")
	}
}

// TestManager_GetOrCreate_DialError 验证拨号失败时返回错误且不留垃圾 session。
func TestManager_GetOrCreate_DialError(t *testing.T) {
	d := &stubDialer{err: context.DeadlineExceeded}
	m := newTestManager(t, d)

	if _, err := m.GetOrCreate(t.Context(), "cookie-a"); err == nil {
		t.Fatal("dial error should propagate")
	}
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("failed dial left %d sessions in map, want 0", n)
	}
}

// TestManager_GetOrCreate_IdentityFields 验证 Session 带上 Manager 的身份配置。
func TestManager_GetOrCreate_IdentityFields(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	s, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if s.user != "alice" {
		t.Errorf("user = %q, want alice", s.user)
	}
	if s.convID != "lobby" {
		t.Errorf("convID = %q, want lobby", s.convID)
	}
	if s.device == "" || s.device == "web-unknown" {
		t.Errorf("device = %q, want generated web-<hex>", s.device)
	}
	if s.id != "cookie-a" {
		t.Errorf("session id = %q, want cookie-a", s.id)
	}
}

// ---- Manager.Release / Sweep / CloseAll --------------------------------------

// TestManager_Release_ShutsDown 验证 Release 关闭底层 client 且幂等。
func TestManager_Release_ShutsDown(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	if _, err := m.GetOrCreate(t.Context(), "cookie-a"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	m.Release("cookie-a")
	m.Release("cookie-a") // 幂等：不存在不 panic

	if !d.client(0).closed() {
		t.Error("Release did not close the client")
	}
	m.mu.Lock()
	_, ok := m.sessions["cookie-a"]
	m.mu.Unlock()
	if ok {
		t.Error("Release did not remove session from map")
	}
}

// TestManager_SweepExpired_RemovesIdle 验证超 TTL 无活动的 session 被清扫。
func TestManager_SweepExpired_RemovesIdle(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	s, err := m.GetOrCreate(t.Context(), "cookie-a")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	// 把 lastSeen 拨到 TTL 之前（同包测试可直接碰私有字段，避免 sleep）。
	s.mu.Lock()
	s.lastSeen = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	m.SweepExpired()

	if !d.client(0).closed() {
		t.Error("idle session was not swept (client not closed)")
	}
	m.mu.Lock()
	_, ok := m.sessions["cookie-a"]
	m.mu.Unlock()
	if ok {
		t.Error("idle session still in map after sweep")
	}
}

// TestManager_SweepExpired_KeepsActive 验证 TTL 内有活动的 session 不被扫。
func TestManager_SweepExpired_KeepsActive(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	if _, err := m.GetOrCreate(t.Context(), "cookie-a"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	m.SweepExpired() // 刚建的，lastSeen 是现在

	if d.client(0).closed() {
		t.Error("active session was swept")
	}
}

// TestManager_SweepExpired_RemovesDead 验证 hub 断开的 session 即使没到 TTL
// 也被清扫（janitor 兜底死连接）。
func TestManager_SweepExpired_RemovesDead(t *testing.T) {
	d := &stubDialer{}
	m := newTestManager(t, d)

	if _, err := m.GetOrCreate(t.Context(), "cookie-a"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	close(d.client(0).done)
	m.SweepExpired()

	if !d.client(0).closed() {
		t.Error("dead session was not swept")
	}
}

// TestManager_CloseAll_ShutsDownAll 验证 CloseAll 释放全部 session。
func TestManager_CloseAll_ShutsDownAll(t *testing.T) {
	d := &stubDialer{}
	m := NewManager(ManagerConfig{
		User:          "alice",
		DialTimeout:   time.Second,
		SweepInterval: time.Hour,
	}, d.dial)

	for _, c := range []string{"a", "b", "c"} {
		if _, err := m.GetOrCreate(t.Context(), c); err != nil {
			t.Fatalf("GetOrCreate %s: %v", c, err)
		}
	}
	m.CloseAll()
	m.CloseAll() // 幂等

	for i := range d.clients {
		if !d.client(i).closed() {
			t.Errorf("client %d not closed after CloseAll", i)
		}
	}
}
