package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// ---- i18n 测试替身 -----------------------------------------------------------

// testTranslator 返回 zh-cn bundle：webui 测试断言沿用中文文案，
// 与生产 locale 探测解耦。bundle 是 embed 只读的，每次现取无副作用。
func testTranslator() i18n.Translator {
	return i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en").ForLocale("zh-cn")
}

// fakeTranslator 记录所有被查询的 key 并返回 "T:"+key 前缀串，
// 供「该渲染的文案都走了 i18n」类断言使用。
type fakeTranslator struct {
	mu     sync.Mutex
	called []string
}

func (f *fakeTranslator) T(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, key)
	return "T:" + key
}

func (f *fakeTranslator) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.called...)
}

// ---- 测试替身 ---------------------------------------------------------------

// sendRecord 记录一次 SendMessage 调用入参。
type sendRecord struct {
	convID string
	body   string
}

// stubClient 是 Client 接口的测试替身：记录 SendMessage 入参，
// Subscribe 返回共享的注入用 channel，History 返回预置消息。
type stubClient struct {
	mu      sync.Mutex
	sends   []sendRecord
	sendErr error

	history []protocol.StoredMessage
	histErr error

	// fetchResp/fetchErr 是 FetchHistory（/history 分页端点）的预置返回。
	// fetchBefore 记录最后一次调用的 before 游标，供断言分页参数。
	fetchResp   protocol.HistoryResponse
	fetchErr    error
	fetchBefore uint64

	events chan core.Event
	done   chan struct{}

	closeCount int
}

func newStubClient() *stubClient {
	return &stubClient{
		events: make(chan core.Event, 16),
		done:   make(chan struct{}),
	}
}

func (s *stubClient) SendMessage(_ context.Context, convID, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sends = append(s.sends, sendRecord{convID: convID, body: body})
	return nil
}

func (s *stubClient) Subscribe(_ int) core.Subscription {
	return &stubSubscription{c: s.events}
}

// History 返回预置消息中 ServerSeq 严格大于 after 的子集（升序），
// 与真实 Store 语义一致，供首屏渲染与重连补发（catchUp）共用。
func (s *stubClient) History(_ context.Context, _ string, after uint64, _ int) ([]protocol.StoredMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.histErr != nil {
		return nil, s.histErr
	}
	var out []protocol.StoredMessage
	for _, m := range s.history {
		if m.ServerSeq > after {
			out = append(out, m)
		}
	}
	return out, nil
}

// FetchHistory 记录 before 游标并返回预置的分页响应。
func (s *stubClient) FetchHistory(_ context.Context, _ string, _, before uint64, _ int) (protocol.HistoryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchBefore = before
	if s.fetchErr != nil {
		return protocol.HistoryResponse{}, s.fetchErr
	}
	return s.fetchResp, nil
}

func (s *stubClient) lastFetchBefore() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchBefore
}

func (s *stubClient) Done() <-chan struct{} { return s.done }

// Close 记录关闭次数并幂等关闭 done（Manager 回收 Session 时调用）。
func (s *stubClient) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCount++
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func (s *stubClient) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCount > 0
}

// sentCount 供断言用（sends 由 handler goroutine 写，加锁读）。
func (s *stubClient) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sends)
}

type stubSubscription struct {
	c <-chan core.Event
}

func (s *stubSubscription) C() <-chan core.Event { return s.c }

// Close 按 EventBus 契约不关闭 channel（只摘订阅者），stub 里是无操作。
func (s *stubSubscription) Close() error { return nil }

// stubDialer 是 Dialer 的测试替身：每次拨号 new 一个 stubClient 并计数。
// history / histErr / sendErr 是模板，拨号时复制给新 client。
type stubDialer struct {
	mu      sync.Mutex
	count   int
	clients []*stubClient
	err     error

	history []protocol.StoredMessage
	histErr error
	sendErr error

	// fetchResp/fetchErr 复制给每个新 client 的 FetchHistory 预置返回。
	fetchResp protocol.HistoryResponse
	fetchErr  error
}

func (d *stubDialer) dial(_ context.Context, _ DialOptions) (Client, core.Store, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.count++
	if d.err != nil {
		return nil, nil, d.err
	}
	cli := newStubClient()
	cli.history = d.history
	cli.histErr = d.histErr
	cli.sendErr = d.sendErr
	cli.fetchResp = d.fetchResp
	cli.fetchErr = d.fetchErr
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
		Translator:    testTranslator(),
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
