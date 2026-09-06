package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	"github.com/pandaymx/lanchat/pkg/transport/fake"
)

// ---- 测试替身 -------------------------------------------------------------

// stubClient 是 Client 接口的测试替身：记录 SendMessage 入参，
// Subscribe 返回共享的注入用 channel，History 返回预置消息。
type stubClient struct {
	mu      sync.Mutex
	sends   []sendRecord
	sendErr error

	history []protocol.StoredMessage
	histErr error

	events chan core.Event
	done   chan struct{}
}

type sendRecord struct {
	convID string
	body   string
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

func (s *stubClient) History(_ context.Context, _ string, _ uint64, _ int) ([]protocol.StoredMessage, error) {
	return s.history, s.histErr
}

func (s *stubClient) Done() <-chan struct{} { return s.done }

// sentCount / lastSend 供断言用（sends 由 handler goroutine 写，加锁读）。
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

// newTestHandler 造一个 stub 驱动的 Handler 供测试用。
func newTestHandler() (*Handler, *stubClient) {
	cli := newStubClient()
	h := NewHandler(Config{
		User:    "alice",
		Device:  "web-test",
		ConvID:  "lobby",
		Version: "test",
	}, cli)
	return h, cli
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

// startSSE 在已有 server 上打开 /events 流。
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

// startTestSSE 起一个挂好 h 路由的 server 并打开 /events 流，返回帧读取器。
//
// 清理顺序是这里的关键坑：t.Cleanup 逆序执行，必须先关 SSE 响应体 / 取消
// 请求 ctx（eventLoop 收到断开才返回），再 srv.Close()——反过来 Server.Close
// 会等活跃连接直到测试超时（10 分钟 default timeout，pre-push 卡 600s 的元凶）。
// 因此 srv.Close 必须先注册（最后执行），startSSE 的 cancel / body 后注册（先执行）。
// 调用方千万不要对返回前注册的 server 再 defer srv.Close()。
func startTestSSE(t *testing.T, h *Handler) *bufio.Reader {
	t.Helper()
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return startSSE(t, srv)
}

// ---- handleHome ------------------------------------------------------------

// TestHandleHome_RendersShell 验证 GET / 返回 200 且含页面骨架。
func TestHandleHome_RendersShell(t *testing.T) {
	h, _ := newTestHandler()
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
		"alice@web-test", // PageMeta.Who() 拼出的身份串
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestHandleHome_ContentType 验证首页 Content-Type 带 charset=utf-8。
//
// 少了 charset 会让中文消息在某些浏览器里乱码（meta 里有 charset 兜底，
// 但 header 更权威，两者都给最稳）。
func TestHandleHome_ContentType(t *testing.T) {
	h, _ := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Content-Type")
	if !strings.Contains(got, "text/html") || !strings.Contains(got, "charset=utf-8") {
		t.Fatalf("Content-Type = %q, want text/html with charset=utf-8", got)
	}
}

// TestHandleHome_RendersHistory 验证首页用 History 的结果填充消息列表，
// 且 Self 判定按 SenderUser 与 Config.User 比对。
func TestHandleHome_RendersHistory(t *testing.T) {
	h, cli := newTestHandler()
	cli.history = []protocol.StoredMessage{
		{ID: "m1", ServerSeq: 1, ConversationID: "lobby", SenderUserID: "alice", Body: "self msg", CreatedAt: 1700000000000},
		{ID: "m2", ServerSeq: 2, ConversationID: "lobby", SenderUserID: "bob", Body: "other msg", CreatedAt: 1700000001000},
	}
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
	h, cli := newTestHandler()
	cli.histErr = context.DeadlineExceeded
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 (history error must not fail the page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "历史加载失败") {
		t.Error("body missing error banner")
	}
}

// TestHandleHome_UnknownPath404 验证非根路径返回 404。
//
// ServeMux 的 "/" 是前缀匹配，会把 /whatever 也交给 handleHome；
// 不显式判等就会渲染出首页，掩盖 URL 拼写错误。
func TestHandleHome_UnknownPath404(t *testing.T) {
	h, _ := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/whatever", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /whatever status = %d, want 404", rec.Code)
	}
}

// TestHandleHome_PostNotAllowed 验证 GET-only 约束。
func TestHandleHome_PostNotAllowed(t *testing.T) {
	h, _ := newTestHandler()
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
	h, cli := newTestHandler()
	rec := httptest.NewRecorder()

	h.handleMessages(rec, postForm("hello"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /messages status = %d, want 204", rec.Code)
	}
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
	h, _ := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleMessages(rec, httptest.NewRequest(http.MethodGet, "/messages", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /messages status = %d, want 405", rec.Code)
	}
}

// TestHandleMessages_EmptyBody400 验证空白消息被拒且不转发。
func TestHandleMessages_EmptyBody400(t *testing.T) {
	for _, body := range []string{"", "%20%20"} {
		h, cli := newTestHandler()
		rec := httptest.NewRecorder()
		h.handleMessages(rec, postForm(body))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%q status = %d, want 400", body, rec.Code)
		}
		if cli.sentCount() != 0 {
			t.Errorf("body=%q: SendMessage should not be called", body)
		}
	}
}

// TestHandleMessages_SendError500 验证转发失败返回 500。
func TestHandleMessages_SendError500(t *testing.T) {
	h, cli := newTestHandler()
	cli.sendErr = context.DeadlineExceeded
	rec := httptest.NewRecorder()

	h.handleMessages(rec, postForm("hello"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// ---- handleEvents ----------------------------------------------------------

// TestHandleEvents_StreamsHeartbeat 验证 SSE 端点：
//  1. 响应头是 text/event-stream / no-cache
//  2. 立刻收到首帧（`: lanchat sse ready`），不等第一个心跳周期
func TestHandleEvents_StreamsHeartbeat(t *testing.T) {
	h, _ := newTestHandler()

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
	h, cli := newTestHandler()

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil { // 跳过 ready 注释帧
		t.Fatalf("read ready frame: %v", err)
	}

	cli.events <- core.Event{
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
	h, cli := newTestHandler()

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	cli.events <- core.Event{
		Kind:           core.EventMessage,
		ConversationID: "other-conv",
		Message:        &protocol.StoredMessage{ID: "m9", ServerSeq: 1, ConversationID: "other-conv", SenderUserID: "bob", Body: "spam"},
	}
	// 不发会导致下一读阻塞 —— 用短超时证明"没有帧"。这里直接验证
	// sseFrame 的纯函数路径更直接：走 handler 内部逻辑需真实流,
	// 所以改为读下一帧前注入一个本会话事件，断言流里只有它。
	cli.events <- core.Event{
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
	h, cli := newTestHandler()

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	cli.events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: false}}
	frame, err := readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read state frame: %v", err)
	}
	if frame != "event: state\ndata: disconnected\n\n" {
		t.Errorf("state frame = %q", frame)
	}

	cli.events <- core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}}
	frame, err = readSSEFrame(reader)
	if err != nil {
		t.Fatalf("read state frame: %v", err)
	}
	if frame != "event: state\ndata: connected\n\n" {
		t.Errorf("state frame = %q", frame)
	}
}

// TestHandleEvents_EndsOnHubLoss 验证 hub 连接断开（cli.Done 关闭）时
// SSE 流结束 —— 浏览器 EventSource 收到 EOF 后会自动重连本端点。
func TestHandleEvents_EndsOnHubLoss(t *testing.T) {
	h, cli := newTestHandler()

	reader := startTestSSE(t, h)
	if _, err := readSSEFrame(reader); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	close(cli.done)
	// 流应当很快结束：读到 EOF / 错误即为通过。
	if _, err := readSSEFrame(reader); err == nil {
		t.Error("stream did not end after hub loss")
	}
}

// TestRoutes_MountsAllEndpoints 验证 Routes 把四条路由都挂上了
// （三条业务 + /assets/ 静态资源）。
func TestRoutes_MountsAllEndpoints(t *testing.T) {
	h, _ := newTestHandler()
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

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

// ---- 端到端（fake hub + 两 web 实例）---------------------------------------

// TestEndToEnd_TwoWebInstancesExchangeMessages 走完整链路验证 M4.3 验收：
//
//	fake hub ← DialClient(alice/web-a) + DialClient(bob/web-b)
//	web-a 的 SSE 流 ↔ web-a 的 EventBus（hub 广播回环含发送方）
//	POST /messages → Client.SendMessage → hub 广播 → 双方 SSE 都收到
//
// hub 用 fake 实现（Router 协议语义与真 WebSocket Hub 同一份代码），
// 真网络的对应回归由 internal/integration 覆盖。
func TestEndToEnd_TwoWebInstancesExchangeMessages(t *testing.T) {
	ftr := fake.New()
	defer ftr.Close()
	hub := ftr.NewHub("test")
	hub.AttachStore(memory.New())

	dial := func(user, device string) (*client.Client, core.Store) {
		t.Helper()
		cli, store, err := DialClient(t.Context(), DialOptions{
			Transport: ftr,
			HubURL:    "memory://test",
			User:      user,
			Device:    device,
		})
		if err != nil {
			t.Fatalf("dial %s: %v", user, err)
		}
		t.Cleanup(func() {
			_ = cli.Close()
			_ = store.Close()
		})
		return cli, store
	}

	cliA, _ := dial("alice", "web-a")
	cliB, _ := dial("bob", "web-b")

	newSrv := func(user, device string, cli Client) *httptest.Server {
		t.Helper()
		h := NewHandler(Config{User: user, Device: device, ConvID: "lobby", Version: "test"}, cli)
		mux := http.NewServeMux()
		h.Routes(mux)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv
	}
	srvA := newSrv("alice", "web-a", cliA)
	srvB := newSrv("bob", "web-b", cliB)

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
