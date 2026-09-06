package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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
	h := NewHandler(Config{Version: "test"}, mgr)
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
func startTestSSE(t *testing.T, h *Handler) *bufio.Reader {
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

	return startSSE(t, srv)
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

// TestHandleEvents_DeliversStateFrame 验证 EventState → `event: state` 帧。
func TestHandleEvents_DeliversStateFrame(t *testing.T) {
	h, d := newTestHandler(t, nil)

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	d.client(0).events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: false}}
	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read state frame: %v", err)
	}
	if frame != "event: state\ndata: disconnected\n\n" {
		t.Errorf("state frame = %q", frame)
	}

	d.client(0).events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}}
	frame, err = readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read state frame: %v", err)
	}
	if frame != "event: state\ndata: connected\n\n" {
		t.Errorf("state frame = %q", frame)
	}
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
