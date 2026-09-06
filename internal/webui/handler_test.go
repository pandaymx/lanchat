package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	"github.com/pandaymx/lanchat/pkg/transport/fake"
)

// ---- 测试装配 ---------------------------------------------------------------

// newTestHandler 造一个 stub dialer 驱动的 Handler（Manager 按 cookie 惰性
// 拨号，dialer 计数 / 返回的 stubClient 供断言）。
func newTestHandler(t *testing.T, d *stubDialer) (*Handler, *stubDialer) {
	t.Helper()
	if d == nil {
		d = &stubDialer{}
	}
	mgr := newTestManager(t, d)
	h := NewHandler(Config{Version: "test", Translator: testTranslator()}, mgr)
	return h, d
}

// ---- SSE 流式读取辅助 ------------------------------------------------------

// readSSEFrame 从流里读一条完整 SSE 帧（到空行为止）。
// 注释行（":" 开头）也算独立帧，由调用方自行跳过。
func readSSEFrame(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(line)
		if line == "\n" || line == "\r\n" {
			return sb.String(), nil
		}
	}
}

// startSSE 在已有 server 上打开 /events 流（复用 srv.Client 的 cookie jar）。
func startSSE(t *testing.T, srv *httptest.Server) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	return bufio.NewReader(resp.Body)
}

// startTestSSE 起一个挂好 h 路由的 server，先 GET / 种 cookie（触发惰性
// 拨号建 session），再打开 /events 流，返回帧读取器。
//
// 清理顺序是这里的关键坑：t.Cleanup 逆序执行，必须先关 SSE 响应体 / 取消
// 请求 ctx（serveSSE 收到断开才返回），再 srv.Close()——反过来 Server.Close
// 会等活跃连接直到测试超时。因此 srv.Close 必须先注册（最后执行）。
// cookie jar 保证 GET / 签发的 lanchat_session 在 /events 请求上带上，
// 两条请求落到同一份 Session（多 tab 共享的测试前提）。
// startTestServer 起挂好 h 路由的 server：配 cookie jar 并先 GET / 种
// cookie（触发惰性拨号建 session）。后续所有请求（含多条 /events）共用
// 同一 jar → 同一 cookie → 同一 Session，这是多 tab 测试的前提。
//
// 清理顺序坑：t.Cleanup 逆序执行，srv.Close 先注册（最后执行），
// SSE 的 cancel / body.Close 后注册（先执行），互等死锁见 startSSE 注释。
func startTestServer(t *testing.T, h *Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	srv.Client().Jar = jar

	// 种 cookie + 建立 session（/events 首请求也会签发，但提前 GET / 让
	// 测试在连 SSE 前就能拿到 d.client(0) 注入事件）。
	resp, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("seed GET /: %v", err)
	}
	_ = resp.Body.Close()
	return srv
}

// startTestSSE 起 server 并打开一条 /events 流。
func startTestSSE(t *testing.T, h *Handler) *bufio.Reader {
	t.Helper()
	return startSSE(t, startTestServer(t, h))
}

// openSSEWithCtx 用独立 ctx 打开 /events 流（测试取消 ctx 模拟关 tab）。
// 返回 reader 与响应体；调用方负责取消 ctx / 关闭 body。
func openSSEWithCtx(t *testing.T, srv *httptest.Server) (context.CancelFunc, *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	// 清理顺序：body.Close 先于 srv.Close（Cleanup 逆序，srv.Close 已先注册）。
	t.Cleanup(func() { _ = resp.Body.Close() })
	return cancel, bufio.NewReader(resp.Body)
}

// readFrameTimeout 在 timeout 内读一条完整 SSE 帧，超时即失败。
func readFrameTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type frameResult struct {
		frame string
		err   error
	}
	ch := make(chan frameResult, 1)
	go func() {
		f, err := readSSEFrame(r)
		ch <- frameResult{frame: f, err: err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read sse frame: %v", res.err)
		}
		return res.frame
	case <-time.After(timeout):
		t.Fatalf("no sse frame within %v", timeout)
	}
	return ""
}

// skipReadyFrame 跳过首帧 `: lanchat sse ready` 注释。
func skipReadyFrame(t *testing.T, r *bufio.Reader) {
	t.Helper()
	frame := readFrameTimeout(t, r, 2*time.Second)
	if !strings.Contains(frame, "lanchat sse ready") {
		t.Fatalf("first frame = %q, want ready comment", frame)
	}
}

// expectNoFrame 断言窗口期内没有任何 SSE 帧到达（验证不补发/帧已被过滤）。
// 超时后读 goroutine 仍挂在流上，连接 cleanup 关 body 时自然退出。
func expectNoFrame(t *testing.T, r *bufio.Reader, timeout time.Duration) {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		f, err := readSSEFrame(r)
		if err == nil {
			ch <- f
		}
	}()
	select {
	case f := <-ch:
		t.Fatalf("expected no frame within %v, got %q", timeout, f)
	case <-time.After(timeout):
	}
}

// startSSEWithLastID 打开 /events 流并携带 Last-Event-ID 头
// （模拟浏览器 EventSource 断线重连）。lastID 为空则不带头。
func startSSEWithLastID(t *testing.T, srv *httptest.Server, lastID string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return bufio.NewReader(resp.Body)
}

// fanoutMsgEvent 造一条本会话的消息事件供 stub client 注入。
func fanoutMsgEvent(seq uint64, id, body string) core.Event {
	return core.Event{
		Kind:           core.EventMessage,
		ConversationID: "lobby",
		Message: &protocol.StoredMessage{
			ID: id, ServerSeq: seq, ConversationID: "lobby",
			SenderUserID: "bob", Body: body, CreatedAt: 1700000000000,
		},
	}
}

// ---- handleHome ------------------------------------------------------------

// TestHandleHome_RendersShell 验证 GET / 返回 200 且含页面骨架。
func TestHandleHome_RendersShell(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.handleHome(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// 注意：templ 会把 `<!DOCTYPE html>` 规范化成小写 `<!doctype html>`
	// （HTML 规范里 DOCTYPE 大小写不敏感）。断言按实际产物写小写，
	// 不要"修"回大写——那会让测试永远 fail。
	for _, want := range []string{
		"<!doctype html>",
		`class="app"`,
		`hx-sse="connect:/events"`,
		`hx-sse="swap:message"`, // M4.3：#messages 挂上 SSE swap 目标
		"/assets/htmx.min.js",
		"/assets/style.css",
		"alice@web-", // PageMeta.Who() 拼出的身份串；device 由 Manager 生成 web-<hex>
		// M4.5：Enter 发送 / Shift+Enter 换行 / 输入法组词保护
		"hx-on:keydown=",
		"event.key==='Enter'",
		"event.isComposing",
		"requestSubmit()",
		// M4.5：连接状态区挂 SSE state swap
		`id="conn-state"`,
		`hx-sse="swap:state"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestHandleHome_IssuesCookie 验证首次访问（无 cookie）会签发会话 cookie。
func TestHandleHome_IssuesCookie(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && len(c.Value) == 16 {
			found = true
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
		}
	}
	if !found {
		t.Error("GET / did not issue a session cookie")
	}
}

// TestHandleHome_ContentType 验证首页 Content-Type 带 charset=utf-8。
//
// 少了 charset 会让中文消息在某些浏览器里乱码（meta 里有 charset 兜底，
// 但 header 更权威，两者都给最稳）。
func TestHandleHome_ContentType(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Content-Type")
	if !strings.Contains(got, "text/html") || !strings.Contains(got, "charset=utf-8") {
		t.Fatalf("Content-Type = %q, want text/html with charset=utf-8", got)
	}
}

// TestHandleHome_RendersHistory 验证首页用 History 的结果填充消息列表，
// 且 Self 判定按 SenderUser 与 session 身份比对。
func TestHandleHome_RendersHistory(t *testing.T) {
	d := &stubDialer{
		history: []protocol.StoredMessage{
			{ID: "m1", ServerSeq: 1, ConversationID: "lobby", SenderUserID: "alice", Body: "self msg", CreatedAt: 1700000000000},
			{ID: "m2", ServerSeq: 2, ConversationID: "lobby", SenderUserID: "bob", Body: "other msg", CreatedAt: 1700000001000},
		},
	}
	h, _ := newTestHandler(t, d)
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	for _, want := range []string{
		`id="msg-m1"`, "self msg", `msg-sender self`, // 本人消息带 self 样式
		`id="msg-m2"`, "other msg", // 他人消息不带
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestHandleHome_HistoryErrorShowsBanner 验证 History 失败时渲染错误 banner
// 而不是 500 —— 历史拉不到不该把整页打死。
func TestHandleHome_HistoryErrorShowsBanner(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{histErr: context.DeadlineExceeded})
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 (history error must not fail the page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "历史加载失败") {
		t.Error("body missing error banner")
	}
}

// TestHandleHome_DialFailure503 验证 hub 拨号失败时回 503 而非 200/500。
func TestHandleHome_DialFailure503(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{err: context.DeadlineExceeded})
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET / status = %d, want 503", rec.Code)
	}
}

// TestHandleHome_UnknownPath404 验证非根路径返回 404。
//
// ServeMux 的 "/" 是前缀匹配，会把 /whatever 也交给 handleHome；
// 不显式判等就会渲染出首页，掩盖 URL 拼写错误。
func TestHandleHome_UnknownPath404(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/whatever", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /whatever status = %d, want 404", rec.Code)
	}
}

// TestHandleHome_PostNotAllowed 验证 GET-only 约束。
func TestHandleHome_PostNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", rec.Code)
	}
}

// TestTemplate_EscapesMessageBody 验证消息体里的 HTML 被转义（XSS 防护）。
//
// 提案 R9 明确点出这一条：Body 是用户输入，必须走 templ 的自动转义。
func TestTemplate_EscapesMessageBody(t *testing.T) {
	data := templates.HomeData{
		Meta: templates.PageMeta{Title: "t", User: "alice", Device: "web"},
		Messages: []templates.MessageView{
			templates.NewMessageView("1", 1, "bob", "<script>alert(1)</script>", 0, false),
		},
	}
	var sb strings.Builder
	if err := templates.Home(data).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := sb.String()
	if strings.Contains(got, "<script>alert(1)</script>") {
		t.Error("message body was not HTML-escaped (XSS risk)")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("expected escaped &lt;script&gt; in output")
	}
}

// TestTemplate_ConnStatus 验证断连渲染 banner、已连接渲染空（state 帧 swap 清屏）。
func TestTemplate_ConnStatus(t *testing.T) {
	tr := testTranslator()
	var sb strings.Builder
	if err := templates.ConnStatus(tr, false).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render disconnected: %v", err)
	}
	got := sb.String()
	if !strings.Contains(got, "连接已断开") || !strings.Contains(got, `class="banner offline"`) {
		t.Errorf("disconnected banner missing: %q", got)
	}

	sb.Reset()
	if err := templates.ConnStatus(tr, true).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render connected: %v", err)
	}
	if strings.TrimSpace(sb.String()) != "" {
		t.Errorf("connected must render empty to clear banner, got %q", sb.String())
	}
}

// TestWebI18N_AllChromeKeysUsed 用 fakeTranslator 收集一次完整渲染里被
// 查询的 key，断言 6 个 web.* chrome key 全部走到——防止新增硬编码中文
// 文案绕开 i18n，也防止模板引用了 bundle 里不存在的 key（fake 对任意
// key 都返回，bundle 侧完整性由 agents-check 钩子 + cmd/tui E2E 兜底）。
func TestWebI18N_AllChromeKeysUsed(t *testing.T) {
	tr := &fakeTranslator{}

	// 1) 首页：空消息 + 有更多历史 → composer/placeholder/send、empty、load_more。
	home := templates.HomeData{
		Meta:    templates.PageMeta{Title: "t", User: "alice", Device: "web"},
		HasMore: true,
		Tr:      tr,
	}
	var sb strings.Builder
	if err := templates.Home(home).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render home: %v", err)
	}

	// 2) 断连 banner → state.disconnected。
	sb.Reset()
	if err := templates.ConnStatus(tr, false).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render conn status: %v", err)
	}

	// 3) HistoryPage 带 LoadMore → 再命中一次 load_more（片段路径）。
	sb.Reset()
	if err := templates.HistoryPage(tr, nil, true, 42).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render history page: %v", err)
	}

	// 3.5) 成员条带自己 → peers.title + peers.you（M7.2）。
	sb.Reset()
	if err := templates.Peers(tr, []templates.PeerView{{User: "alice", Device: "d1", Self: true}}).Render(t.Context(), &sb); err != nil {
		t.Fatalf("render peers: %v", err)
	}

	// 4) 历史错误 banner → web.history.error（走 handler 真实路径）。
	mgr := newTestManager(t, &stubDialer{histErr: context.DeadlineExceeded})
	h := NewHandler(Config{Version: "test", Translator: tr}, mgr)
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]bool{
		"web.composer.placeholder": false,
		"web.composer.send":        false,
		"web.messages.empty":       false,
		"web.history.load_more":    false,
		"web.state.disconnected":   false,
		"web.history.error":        false,
		"web.peers.title":          false,
		"web.peers.empty":          false,
		"web.peers.you":            false,
	}
	for _, k := range tr.keys() {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, hit := range want {
		if !hit {
			t.Errorf("i18n key %q never queried during render", k)
		}
	}
}

// ---- handleMessages --------------------------------------------------------

// postForm 造一个 POST /messages 请求。
func postForm(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader("body="+body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// TestHandleMessages_Returns204 验证发消息返回 204（HTMX 不做 DOM 替换）。
func TestHandleMessages_Returns204(t *testing.T) {
	h, d := newTestHandler(t, nil)
	rec := httptest.NewRecorder()

	h.handleMessages(rec, postForm("hello"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /messages status = %d, want 204", rec.Code)
	}
	cli := d.client(0)
	if cli.sentCount() != 1 {
		t.Fatalf("SendMessage called %d times, want 1", cli.sentCount())
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if cli.sends[0].convID != "lobby" || cli.sends[0].body != "hello" {
		t.Errorf("SendMessage args = %+v, want conv=lobby body=hello", cli.sends[0])
	}
}

// TestHandleMessages_RejectsGet 验证 GET /messages 被拒。
func TestHandleMessages_RejectsGet(t *testing.T) {
	h, d := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.handleMessages(rec, httptest.NewRequest(http.MethodGet, "/messages", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /messages status = %d, want 405", rec.Code)
	}
	if d.dialCount() != 0 {
		t.Errorf("dial count = %d, want 0 (method check must come before dial)", d.dialCount())
	}
}

// TestHandleMessages_EmptyBody400 验证空白消息被拒且不拨号、不转发。
func TestHandleMessages_EmptyBody400(t *testing.T) {
	for _, body := range []string{"", "%20%20"} {
		h, d := newTestHandler(t, nil)
		rec := httptest.NewRecorder()
		h.handleMessages(rec, postForm(body))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%q status = %d, want 400", body, rec.Code)
		}
		if d.dialCount() != 0 {
			t.Errorf("body=%q: dial happened before validation", body)
		}
	}
}

// TestHandleMessages_SendError500 验证转发失败返回 500。
func TestHandleMessages_SendError500(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{sendErr: context.DeadlineExceeded})
	rec := httptest.NewRecorder()

	h.handleMessages(rec, postForm("hello"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestHandleMessages_DialFailure503 验证拨号失败回 503。
func TestHandleMessages_DialFailure503(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{err: context.DeadlineExceeded})
	rec := httptest.NewRecorder()

	h.handleMessages(rec, postForm("hello"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// ---- handleHistory ---------------------------------------------------------

// olderMsgs 构造一页"更早"的消息（按 seq 升序）。
func olderMsgs(seqs ...uint64) []protocol.StoredMessage {
	out := make([]protocol.StoredMessage, 0, len(seqs))
	for _, s := range seqs {
		out = append(out, protocol.StoredMessage{
			ID:             "m" + strconv.FormatUint(s, 10),
			ConversationID: "lobby",
			ServerSeq:      s,
			SenderUserID:   "alice",
			Body:           "older-" + strconv.FormatUint(s, 10),
		})
	}
	return out
}

// TestHandleHistory_RendersOlderPage 验证分页端点：before 透传给 client，
// 响应含消息片段 + 新 LoadMore 按钮（游标为本批最老 seq）。
func TestHandleHistory_RendersOlderPage(t *testing.T) {
	d := &stubDialer{fetchResp: protocol.HistoryResponse{
		Messages: olderMsgs(8, 9), HasMore: true,
	}}
	h, _ := newTestHandler(t, d)

	rec := httptest.NewRecorder()
	h.handleHistory(rec, httptest.NewRequest(http.MethodGet, "/history?before=10", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := d.client(0).lastFetchBefore(); got != 10 {
		t.Errorf("FetchHistory before = %d, want 10", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"older-8", "older-9", "加载更早消息", `/history?before=8`, `hx-swap="outerHTML"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestHandleHistory_NoMoreHidesButton 验证 HasMore=false 时片段不含按钮
// （按钮被消息替换，分页到头）。
func TestHandleHistory_NoMoreHidesButton(t *testing.T) {
	d := &stubDialer{fetchResp: protocol.HistoryResponse{Messages: olderMsgs(1, 2), HasMore: false}}
	h, _ := newTestHandler(t, d)

	rec := httptest.NewRecorder()
	h.handleHistory(rec, httptest.NewRequest(http.MethodGet, "/history?before=3", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "older-1") || !strings.Contains(body, "older-2") {
		t.Errorf("messages missing: %s", body)
	}
	if strings.Contains(body, "加载更早消息") {
		t.Errorf("HasMore=false 不应渲染按钮: %s", body)
	}
}

// TestHandleHistory_BadBefore 验证非法/缺失 before 返回 400 且不拨号。
func TestHandleHistory_BadBefore(t *testing.T) {
	for _, q := range []string{"/history", "/history?before=abc", "/history?before=0"} {
		h, d := newTestHandler(t, nil)
		rec := httptest.NewRecorder()
		h.handleHistory(rec, httptest.NewRequest(http.MethodGet, q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", q, rec.Code)
		}
		if d.dialCount() != 0 {
			t.Errorf("%s: dial happened before validation", q)
		}
	}
}

// TestHandleHistory_RejectsPost 验证 POST /history 被拒（405，且不拨号）。
func TestHandleHistory_RejectsPost(t *testing.T) {
	h, d := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.handleHistory(rec, httptest.NewRequest(http.MethodPost, "/history?before=3", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if d.dialCount() != 0 {
		t.Error("dial happened before method check")
	}
}

// TestHandleHistory_FetchError503 验证拉取失败回 503。
func TestHandleHistory_FetchError503(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{fetchErr: context.DeadlineExceeded})
	rec := httptest.NewRecorder()
	h.handleHistory(rec, httptest.NewRequest(http.MethodGet, "/history?before=10", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestHandleHome_LoadMoreButton 验证首屏拉满 historyLimit 条时渲染
// 「加载更早消息」按钮，游标为最老一条的 seq。
func TestHandleHome_LoadMoreButton(t *testing.T) {
	msgs := make([]protocol.StoredMessage, historyLimit)
	for i := range msgs {
		seq := uint64(100 + i)
		msgs[i] = protocol.StoredMessage{
			ID: "m", ConversationID: "lobby", ServerSeq: seq,
			SenderUserID: "alice", Body: "x",
		}
	}
	h, _ := newTestHandler(t, &stubDialer{history: msgs})

	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "加载更早消息") {
		t.Error("首屏拉满 limit 应渲染加载更多按钮")
	}
	if !strings.Contains(body, "/history?before=100") {
		t.Error("按钮游标应为最老一条 seq=100")
	}
}

// ---- handleEvents ----------------------------------------------------------

// TestHandleEvents_StreamsHeartbeat 验证 SSE 端点：
//  1. 响应头是 text/event-stream / no-cache
//  2. 立刻收到首帧（`: lanchat sse ready`），不等第一个心跳周期
func TestHandleEvents_StreamsHeartbeat(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	reader := startTestSSE(t, h)

	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read sse first frame: %v", err)
	}
	if !strings.Contains(frame, ": lanchat sse ready") {
		t.Errorf("first frame = %q, want ': lanchat sse ready'", frame)
	}
}

// TestHandleEvents_DeliversMessageFrame 验证 EventMessage 被翻译成
// `event: message` + `id: <seq>` + `data: <html>` 帧，且消息体被转义。
func TestHandleEvents_DeliversMessageFrame(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil { // 跳过 ready 注释帧
		t.Fatalf("read ready frame: %v", err)
	}

	d.client(0).events <- core.Event{
		Kind:           core.EventMessage,
		ConversationID: "lobby",
		Message: &protocol.StoredMessage{
			ID: "m1", ServerSeq: 7, ConversationID: "lobby",
			SenderUserID: "bob", Body: "hi <b>x</b>", CreatedAt: 1700000000000,
		},
	}

	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read message frame: %v", err)
	}
	for _, want := range []string{
		"event: message\n",
		"id: 7\n",
		`data: <li id="msg-m1">`,
		"bob",
		"hi &lt;b&gt;x&lt;/b&gt;", // templ 自动转义
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q\ngot: %q", want, frame)
		}
	}
}

// TestHandleEvents_IgnoresForeignConv 验证其它会话的消息不产生帧。
func TestHandleEvents_IgnoresForeignConv(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	d.client(0).events <- core.Event{
		Kind:           core.EventMessage,
		ConversationID: "other-conv",
		Message:        &protocol.StoredMessage{ID: "m9", ServerSeq: 1, ConversationID: "other-conv", SenderUserID: "bob", Body: "spam"},
	}
	// 外会话事件不产生帧；紧跟一个本会话事件，断言流里只有它。
	d.client(0).events <- core.Event{
		Kind:           core.EventMessage,
		ConversationID: "lobby",
		Message:        &protocol.StoredMessage{ID: "m2", ServerSeq: 2, ConversationID: "lobby", SenderUserID: "bob", Body: "real"},
	}

	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if strings.Contains(frame, "spam") || strings.Contains(frame, "msg-m9") {
		t.Errorf("foreign-conv message leaked into stream: %q", frame)
	}
	if !strings.Contains(frame, "real") {
		t.Errorf("own-conv message missing: %q", frame)
	}
}

// TestHandleEvents_DeliversStateFrame 验证 EventState → `event: state` 帧：
// 断连帧带 banner HTML（#conn-state swap 目标），重连帧为空内容（清空 banner）。
func TestHandleEvents_DeliversStateFrame(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	d.client(0).events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: false}}
	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read disconnected frame: %v", err)
	}
	if !strings.Contains(frame, "event: state") || !strings.Contains(frame, "连接已断开") {
		t.Errorf("disconnected frame = %q, want state frame with reconnect banner", frame)
	}

	d.client(0).events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}}
	frame, err = readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read connected frame: %v", err)
	}
	if !strings.Contains(frame, "event: state") {
		t.Errorf("connected frame = %q, want state event", frame)
	}
	if strings.Contains(frame, "连接已断开") {
		t.Errorf("connected frame must clear banner, got %q", frame)
	}
}

// TestParseLastEventID 验证 Last-Event-ID 头解析（缺失/非法/正常）。
func TestParseLastEventID(t *testing.T) {
	for _, tc := range []struct {
		head string
		want uint64
	}{
		{"", 0}, {"abc", 0}, {"0", 0}, {"42", 42}, {"  7 ", 7},
	} {
		req := httptest.NewRequest(http.MethodGet, "/events", nil)
		if tc.head != "" {
			req.Header.Set("Last-Event-ID", tc.head)
		}
		if got := parseLastEventID(req); got != tc.want {
			t.Errorf("Last-Event-ID=%q → %d, want %d", tc.head, got, tc.want)
		}
	}
}

// replayMsgs 构造预置在 stub 本地"Store"里的历史消息。
func replayMsgs(seqs ...uint64) []protocol.StoredMessage {
	out := make([]protocol.StoredMessage, 0, len(seqs))
	for _, s := range seqs {
		out = append(out, protocol.StoredMessage{
			ID: "m" + strconv.FormatUint(s, 10), ServerSeq: s,
			ConversationID: "lobby", SenderUserID: "bob",
			Body: "missed-" + strconv.FormatUint(s, 10),
		})
	}
	return out
}

// TestHandleEvents_ReplaysMissedOnReconnect 验证断线重连补发：
// 携带 Last-Event-ID:5 的连接在 ready 帧后收到本地 Store 中 seq>5
// 的消息帧（6、7），按升序补写。
func TestHandleEvents_ReplaysMissedOnReconnect(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{history: replayMsgs(6, 7)})
	srv := startTestServer(t, h) // seed GET / 建 session

	reader := startSSEWithLastID(t, srv, "5")
	skipReadyFrame(t, reader)

	f6 := readFrameTimeout(t, reader, 2*time.Second)
	if !strings.Contains(f6, "id: 6") || !strings.Contains(f6, "missed-6") {
		t.Errorf("replay frame 1 = %q, want id:6 missed-6", f6)
	}
	f7 := readFrameTimeout(t, reader, 2*time.Second)
	if !strings.Contains(f7, "id: 7") || !strings.Contains(f7, "missed-7") {
		t.Errorf("replay frame 2 = %q, want id:7 missed-7", f7)
	}
}

// TestHandleEvents_CatchUpIsIdempotent 验证补发与实时帧幂等：补发写过
// seq6 后，fanout 通道里同序号帧（补发窗口竞态）被跳过，seq7 正常投递。
func TestHandleEvents_CatchUpIsIdempotent(t *testing.T) {
	d := &stubDialer{history: replayMsgs(6)}
	h, _ := newTestHandler(t, d)
	srv := startTestServer(t, h)

	reader := startSSEWithLastID(t, srv, "5")
	skipReadyFrame(t, reader)

	frame := readFrameTimeout(t, reader, 2*time.Second)
	if !strings.Contains(frame, "id: 6") {
		t.Fatalf("replay frame = %q, want id:6", frame)
	}

	// fanout 再来一条同 seq6（模拟补发窗口内已入缓冲的实时帧）+ 新 seq7。
	d.client(0).events <- fanoutMsgEvent(6, "m6", "missed-6")
	d.client(0).events <- fanoutMsgEvent(7, "m7", "live-7")

	frame = readFrameTimeout(t, reader, 2*time.Second)
	if !strings.Contains(frame, "id: 7") || !strings.Contains(frame, "live-7") {
		t.Errorf("post-catchup frame = %q, want id:7 live-7 (seq6 must be deduped)", frame)
	}
	expectNoFrame(t, reader, 300*time.Millisecond)
}

// TestHandleEvents_NoLastEventIDNoReplay 验证全新连接（无 Last-Event-ID）
// 不补发：ready 帧后窗口期内无消息帧。
func TestHandleEvents_NoLastEventIDNoReplay(t *testing.T) {
	h, _ := newTestHandler(t, &stubDialer{history: replayMsgs(6)})
	srv := startTestServer(t, h)

	reader := startSSE(t, srv)
	skipReadyFrame(t, reader)
	expectNoFrame(t, reader, 300*time.Millisecond)
}

// TestHandleEvents_EndsOnHubLoss 验证 hub 连接断开（cli.Done 关闭）时
// pump 关闭 fanout channel，SSE 流结束 —— 浏览器 EventSource 收到 EOF
// 后会自动重连本端点，重连请求重建 session。
func TestHandleEvents_EndsOnHubLoss(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	close(d.client(0).done)
	// 流应当很快结束：读到 EOF / 错误即为通过。
	if _, err := readSSEFrame(reader); err == nil {
		t.Error("stream did not end after hub loss")
	}
}

// TestRoutes_MountsAllEndpoints 验证 Routes 把四条路由都挂上了
// （三条业务 + /assets/ 静态资源）。
func TestRoutes_MountsAllEndpoints(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	srv.Client().Jar = jar

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/assets/style.css", http.StatusOK},
		{"/assets/htmx.min.js", http.StatusOK},
		{"/assets/htmx-sse.js", http.StatusOK},
		{"/assets/nope.css", http.StatusNotFound},
	} {
		resp, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

// TestHeartbeatInterval_Sane 锁死心跳间隔在合理区间（10s ~ 60s）。
//
// 太小 → 无谓的流量与 CPU；太大 → 中间代理（nginx 默认 60s
// proxy_read_timeout）会把长连接切掉。
func TestHeartbeatInterval_Sane(t *testing.T) {
	if heartbeatInterval < 10*time.Second || heartbeatInterval > 60*time.Second {
		t.Fatalf("heartbeatInterval = %v, want within [10s, 60s]", heartbeatInterval)
	}
}

// ---- 端到端（fake hub + Manager 惰性拨号）-----------------------------------

// TestEndToEnd_TwoWebInstancesExchangeMessages 走完整链路验证 thin proxy：
//
//	fake hub ← Manager 惰性拨号（alice 实例 + bob 实例）
//	浏览器 POST /messages → Session.SendMessage → hub 广播
//	双方 SSE 流（Session pump fanout）都收到回环消息
//
// hub 用 fake 实现（Router 协议语义与真 WebSocket Hub 同一份代码），
// 真网络的对应回归由 internal/integration 覆盖。
func TestEndToEnd_TwoWebInstancesExchangeMessages(t *testing.T) {
	ftr := fake.New()
	defer ftr.Close()
	hub := ftr.NewHub("test")
	hub.AttachStore(memory.New())

	newSrv := func(user string) *httptest.Server {
		t.Helper()
		mgr := NewManager(ManagerConfig{
			HubURL:        "memory://test",
			User:          user,
			ConvID:        "lobby",
			Transport:     ftr,
			DialTimeout:   5 * time.Second,
			SweepInterval: time.Hour, // 关掉 janitor，手动 CloseAll
		}, nil)
		t.Cleanup(mgr.CloseAll)
		h := NewHandler(Config{Version: "test"}, mgr)
		mux := http.NewServeMux()
		h.Routes(mux)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookie jar: %v", err)
		}
		srv.Client().Jar = jar
		return srv
	}
	srvA := newSrv("alice")
	srvB := newSrv("bob")

	// 首次 GET / 触发惰性拨号 + cookie 签发。
	seed := func(srv *httptest.Server) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + "/")
		if err != nil {
			t.Fatalf("seed GET /: %v", err)
		}
		_ = resp.Body.Close()
	}
	seed(srvA)
	seed(srvB)

	// 双方各开一条 SSE 流，goroutine 把帧送进 channel（主 goroutine 消费）。
	readLoop := func(reader *bufio.Reader) chan string {
		ch := make(chan string, 32)
		go func() {
			defer close(ch)
			for {
				frame, err := readSSEFrame(reader)
				if err != nil {
					return
				}
				select {
				case ch <- frame:
				case <-t.Context().Done():
					return
				}
			}
		}()
		return ch
	}
	chA := readLoop(startSSE(t, srvA))
	chB := readLoop(startSSE(t, srvB))

	// alice 发一条消息。
	resp, err := srvA.Client().Post(srvA.URL+"/messages", "application/x-www-form-urlencoded", strings.NewReader("body=hello from alice"))
	if err != nil {
		t.Fatalf("POST /messages: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /messages status = %d, want 204", resp.StatusCode)
	}

	// 断言 A、B 的 SSE 流在 3s 内都收到 message 帧（hub 广播含发送方回环）。
	waitMessage := func(ch chan string, who string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case frame, ok := <-ch:
				if !ok {
					t.Fatalf("%s: sse stream ended before message frame", who)
				}
				if strings.Contains(frame, "event: message") && strings.Contains(frame, "hello from alice") {
					return
				}
			case <-deadline:
				t.Fatalf("%s: no message frame within 3s", who)
			}
		}
	}
	waitMessage(chA, "alice/web-a")
	waitMessage(chB, "bob/web-b")
}

// ---- 多 tab 共享 Session（M4.4 验收）---------------------------------------

// TestFanout_MultipleTabsShareSession 验证同一 cookie 开两条 SSE（两个 tab）
// 只拨号一次，且一条事件两路都收到——多 tab 共享 Session 的核心验收。
func TestFanout_MultipleTabsShareSession(t *testing.T) {
	h, d := newTestHandler(t, nil)
	srv := startTestServer(t, h)

	r1 := startSSE(t, srv)
	r2 := startSSE(t, srv)
	skipReadyFrame(t, r1)
	skipReadyFrame(t, r2)

	if d.dialCount() != 1 {
		t.Fatalf("dial count = %d, want 1 (two tabs share one session)", d.dialCount())
	}

	d.client(0).events <- fanoutMsgEvent(1, "m1", "tab broadcast")

	for i, r := range []*bufio.Reader{r1, r2} {
		frame := readFrameTimeout(t, r, 3*time.Second)
		if !strings.Contains(frame, "event: message") || !strings.Contains(frame, "tab broadcast") {
			t.Errorf("tab %d frame = %q, want message frame", i+1, frame)
		}
	}
}

// TestFanout_ClosingOneTabDoesNotAffectOthers 验证关掉一个 tab（SSE 断开）
// 后注销其 writer，其余 tab 继续收帧，且 session 不重建（dial 仍 1 次）。
func TestFanout_ClosingOneTabDoesNotAffectOthers(t *testing.T) {
	h, d := newTestHandler(t, nil)
	srv := startTestServer(t, h)

	cancel1, r1 := openSSEWithCtx(t, srv)
	r2 := startSSE(t, srv)
	skipReadyFrame(t, r1)
	skipReadyFrame(t, r2)

	// 关掉 tab1：取消请求 ctx（serveSSE 退出并 defer removeWriter）。
	cancel1()
	// 等 handler goroutine 完成注销。
	time.Sleep(200 * time.Millisecond)

	d.client(0).events <- fanoutMsgEvent(1, "m1", "after tab1 closed")

	frame := readFrameTimeout(t, r2, 3*time.Second)
	if !strings.Contains(frame, "after tab1 closed") {
		t.Errorf("tab2 frame = %q, want continued delivery", frame)
	}
	if d.dialCount() != 1 {
		t.Errorf("dial count = %d, want 1 (closing a tab must not rebuild session)", d.dialCount())
	}
}

// TestEndToEnd_MultipleTabsShareSession 走 fake hub 端到端验证：alice 开两个
// tab（同 jar 共享 session），bob 一个；alice 发消息后三流都收到回环广播。
func TestEndToEnd_MultipleTabsShareSession(t *testing.T) {
	ftr := fake.New()
	defer ftr.Close()
	hub := ftr.NewHub("test")
	hub.AttachStore(memory.New())

	newSrv := func(user string) *httptest.Server {
		t.Helper()
		mgr := NewManager(ManagerConfig{
			HubURL:        "memory://test",
			User:          user,
			ConvID:        "lobby",
			Transport:     ftr,
			DialTimeout:   5 * time.Second,
			SweepInterval: time.Hour,
		}, nil)
		t.Cleanup(mgr.CloseAll)
		h := NewHandler(Config{Version: "test"}, mgr)
		mux := http.NewServeMux()
		h.Routes(mux)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookie jar: %v", err)
		}
		srv.Client().Jar = jar
		resp, err := srv.Client().Get(srv.URL + "/")
		if err != nil {
			t.Fatalf("seed GET /: %v", err)
		}
		_ = resp.Body.Close()
		return srv
	}
	srvA := newSrv("alice")
	srvB := newSrv("bob")

	readLoop := func(reader *bufio.Reader) chan string {
		ch := make(chan string, 32)
		go func() {
			defer close(ch)
			for {
				frame, err := readSSEFrame(reader)
				if err != nil {
					return
				}
				select {
				case ch <- frame:
				case <-t.Context().Done():
					return
				}
			}
		}()
		return ch
	}
	// alice 两个 tab（同 jar）+ bob 一个 tab。
	chA1 := readLoop(startSSE(t, srvA))
	chA2 := readLoop(startSSE(t, srvA))
	chB := readLoop(startSSE(t, srvB))

	resp, err := srvA.Client().Post(srvA.URL+"/messages", "application/x-www-form-urlencoded", strings.NewReader("body=multi tab hello"))
	if err != nil {
		t.Fatalf("POST /messages: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /messages status = %d, want 204", resp.StatusCode)
	}

	waitMessage := func(ch chan string, who string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case frame, ok := <-ch:
				if !ok {
					t.Fatalf("%s: sse stream ended before message frame", who)
				}
				if strings.Contains(frame, "event: message") && strings.Contains(frame, "multi tab hello") {
					return
				}
			case <-deadline:
				t.Fatalf("%s: no message frame within 3s", who)
			}
		}
	}
	waitMessage(chA1, "alice tab1")
	waitMessage(chA2, "alice tab2")
	waitMessage(chB, "bob tab1")
}

// TestHandleHome_RendersPeers 验证首屏服务端渲染在线成员条（M7.2）：
// stubClient 预置的 peers 快照出现在 #peers 区块里。
func TestHandleHome_RendersPeers(t *testing.T) {
	d := &stubDialer{
		peers: []protocol.Presence{
			{UserID: "alice", DeviceID: "dev-alice", Online: true},
			{UserID: "carol", DeviceID: "dev-carol", Online: true},
		},
	}
	h, _ := newTestHandler(t, d)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleHome(rec, req)

	body := rec.Body.String()
	for _, want := range []string{`id="peers"`, "peer-chip", "alice", "carol"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %q in peers bar", want)
		}
	}
}

// TestHandleEvents_PresenceFrame 验证 M7.2：EventPresence 被推成 presence
// SSE 帧，片段以 client.Peers() 快照全量重渲。
func TestHandleEvents_PresenceFrame(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil { // 跳过 ready 注释帧
		t.Fatalf("read ready frame: %v", err)
	}

	cli := d.client(0)
	cli.mu.Lock()
	cli.peers = []protocol.Presence{
		{UserID: "alice", DeviceID: "dev-alice", Online: true},
		{UserID: "carol", DeviceID: "dev-carol", Online: true},
	}
	cli.mu.Unlock()

	cli.events <- core.Event{
		Kind:     core.EventPresence,
		Presence: &protocol.Presence{UserID: "carol", DeviceID: "dev-carol", Online: true},
	}

	frame := readFrameTimeout(t, reader, 2*time.Second)
	for _, want := range []string{
		"event: presence\n",
		"alice",
		"carol",
		"peer-chip",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("presence frame missing %q\ngot: %q", want, frame)
		}
	}
}
