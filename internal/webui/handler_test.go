package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
)

// newTestHandler 造一个骨架阶段的 Handler 供测试用。
func newTestHandler() *Handler {
	return NewHandler(Config{
		User:    "alice",
		Device:  "web-test",
		ConvID:  "lobby",
		Version: "test",
	})
}

// TestHandleHome_RendersShell 验证 GET / 返回 200 且含页面骨架。
//
// 这是 M4.2 的最小验收：浏览器能看到页面（提案 §7 M4.2）。
func TestHandleHome_RendersShell(t *testing.T) {
	h := newTestHandler()
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
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Content-Type")
	if !strings.Contains(got, "text/html") || !strings.Contains(got, "charset=utf-8") {
		t.Fatalf("Content-Type = %q, want text/html with charset=utf-8", got)
	}
}

// TestHandleHome_UnknownPath404 验证非根路径返回 404。
//
// ServeMux 的 "/" 是前缀匹配，会把 /whatever 也交给 handleHome；
// 不显式判等就会渲染出首页，掩盖 URL 拼写错误。
func TestHandleHome_UnknownPath404(t *testing.T) {
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodGet, "/whatever", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /whatever status = %d, want 404", rec.Code)
	}
}

// TestHandleHome_PostNotAllowed 验证 GET-only 约束。
func TestHandleHome_PostNotAllowed(t *testing.T) {
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleHome(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want 405", rec.Code)
	}
}

// TestHandleMessages_Returns204 验证发消息返回 204（HTMX 不做 DOM 替换）。
func TestHandleMessages_Returns204(t *testing.T) {
	h := newTestHandler()
	form := strings.NewReader("body=hello")
	req := httptest.NewRequest(http.MethodPost, "/messages", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.handleMessages(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /messages status = %d, want 204", rec.Code)
	}
}

// TestHandleMessages_RejectsGet 验证 GET /messages 被拒。
func TestHandleMessages_RejectsGet(t *testing.T) {
	h := newTestHandler()
	rec := httptest.NewRecorder()
	h.handleMessages(rec, httptest.NewRequest(http.MethodGet, "/messages", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /messages status = %d, want 405", rec.Code)
	}
}

// TestHandleEvents_StreamsHeartbeat 验证 SSE 端点：
//  1. 响应头是 text/event-stream / no-cache
//  2. 立刻收到首帧（`: lanchat sse ready`），不等第一个心跳周期
//
// 注意：handleEvents 会一直阻塞到客户端断开，所以不能直接用
// httptest.NewRecorder 同步调（会挂死测试）。这里起真实 server +
// 带超时的 request context，读到首帧就返回，defer 关 body 让 handler 退出。
func TestHandleEvents_StreamsHeartbeat(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("read sse first frame: %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, ": lanchat sse ready") {
		t.Errorf("first frame = %q, want ': lanchat sse ready'", got)
	}
}

// TestRoutes_MountsAllEndpoints 验证 Routes 把四条路由都挂上了
// （三条业务 + /assets/ 静态资源）。
func TestRoutes_MountsAllEndpoints(t *testing.T) {
	h := newTestHandler()
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
