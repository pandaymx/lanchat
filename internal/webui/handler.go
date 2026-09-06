// Package webui 是 lanchat 的浏览器端（M4）。
//
// 定位（见 docs/proposals/2026-09-05-m4-web-proposal.md §3.2）：
// **thin proxy** —— web server 不内嵌 hubstate.Router，而是用
// `pkg/client.Client` 当一个普通客户端连真 hub。好处：
//   - 业务流与 TUI 端共用一份 pkg/client，零协议逻辑复制
//   - hub 与 web 可独立部署 / 独立重启
//   - internal/integration 的真 hub 测试可零修改复用
//
// 三条 HTTP 流（详见提案 §6.1）：
//
//	GET  /           渲染首页（含历史消息 + SSE 挂载点）
//	POST /messages   发消息（表单 body 字段）
//	GET  /events     SSE 长连接，把 hub 推来的事件推给浏览器
//
// 强约束（AGENTS.md §2）：前端只用 templ + HTMX，不引入 React/Vue
// 等框架，也不引入前端构建工具。htmx.min.js 与 SSE 扩展是 vendored
// 静态文件（见 assets.go），不走 npm / CDN。
package webui

import (
	"context"
	"net/http"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// heartbeatInterval 是 SSE 心跳间隔。
//
// 为什么需要心跳：
//   - 中间代理（nginx 默认 proxy_read_timeout 60s）会以"空闲"为由切断长连接
//   - 浏览器 EventSource 收不到任何字节时无法区分"连接断了"和"没消息"
//
// 15s 远小于 60s，留出两轮重试余量。M5 部署阶段若前面挂了 nginx，
// 仍建议把 proxy_read_timeout 调大（提案 R2）。
const heartbeatInterval = 15 * time.Second

// Config 是 Handler 的构造入参。
//
// M4.2 骨架阶段只有展示用的身份信息；M4.3 接通真 hub 后补 HubURL 等。
type Config struct {
	User    string // 显示名（M4 临时：由 -user flag 给，M6 换成登录）
	Device  string // 设备标识（web-<random>，见提案 §7.1）
	ConvID  string // 会话 ID，默认 lobby
	Version string // ldflags 注入，仅展示用
}

// Handler 持有 web 端所有 HTTP 路由与其依赖。
type Handler struct {
	cfg    Config
	logger *logging.ComponentLogger
}

// NewHandler 构造 Handler。
func NewHandler(cfg Config) *Handler {
	return &Handler{
		cfg:    cfg,
		logger: logging.New("web"),
	}
}

// Routes 把三条业务路由 + 静态资源挂到 mux 上。
//
// 为什么独立成方法而不是在 NewHandler 里直接 new 一个 mux：
// 让调用方（cmd/web/main.go）决定 mux 的生命周期与是否包中间件。
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleHome)
	mux.HandleFunc("/messages", h.handleMessages)
	mux.HandleFunc("/events", h.handleEvents)
	mux.Handle("/assets/", http.StripPrefix("/assets/", StaticHandler()))
}

// handleHome 渲染首页。
//
// M4.2 骨架：消息列表为空、连接状态 false —— 只验证模板与静态资源链路通。
// M4.3 会在这里拉 History 并填充 Messages（提案 §7 M4.3）。
func (h *Handler) handleHome(w http.ResponseWriter, r *http.Request) {
	// 只接受根路径；其它路径一律 404，避免 ServeMux 的 "/" 兜底吃掉拼写错误的 URL。
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := templates.HomeData{
		Meta: templates.PageMeta{
			Title:   "lanchat web",
			User:    h.cfg.User,
			Device:  h.cfg.Device,
			Version: h.cfg.Version,
		},
		Messages:  nil,
		Connected: false,
	}
	h.renderHome(w, r, data)
}

// renderHome 统一渲染入口，供 handleHome 与将来可能的错误渲染复用。
func (h *Handler) renderHome(w http.ResponseWriter, r *http.Request, data templates.HomeData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Home(data).Render(r.Context(), w); err != nil {
		h.logger.Error("render home failed", "err", err)
	}
}

// handleMessages 接收浏览器发来的消息。
//
// M4.2 骨架：只校验并记日志，返回 204 让 HTMX 不刷新页面。
// M4.3 会在这里调 Session.Send → cli.SendMessage（提案 §7 M4.3）。
func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.logger.Warn("parse form failed", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := r.PostFormValue("body")
	h.logger.Info("message received (m4.2 skeleton, not yet forwarded)", "len", len(body))

	// 204 No Content：HTMX 收到后不做任何 DOM 替换，输入框由
	// hx-on::after-request="this.reset()" 清空（见 home.templ）。
	w.WriteHeader(http.StatusNoContent)
}

// handleEvents 是 SSE 长连接端点。
//
// M4.2 骨架：只发心跳注释行（`: ping`），不发真实事件 —— 用来验证
// 浏览器 EventSource 能连上并保持。M4.3 会订阅 Session 的 EventBus
// 并把 core.Event 翻译成 `event: message` / `event: state` 帧。
//
// SSE 帧格式要点（提案附录 B）：
//   - 每条 event 以空行结束
//   - `:` 开头的是注释，浏览器忽略（正合适做心跳）
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 理论上 net/http 的 ResponseWriter 都实现 Flusher；
		// 但如果将来套了 gzip 之类的中间件就可能不是。
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 告诉中间代理这是长连接，别缓冲
	w.Header().Set("X-Accel-Buffering", "no")

	h.logger.Info("sse client connected", "remote", r.RemoteAddr)

	// 先写一个注释行让浏览器立刻拿到响应头（否则要等第一个 ticker）
	if _, err := w.Write([]byte(": lanchat sse ready\n\n")); err != nil {
		h.logger.Warn("sse initial write failed", "err", err)
		return
	}
	flusher.Flush()

	h.heartbeatLoop(r.Context(), w, flusher)
}

// heartbeatLoop 阻塞发心跳直到客户端断开。
//
// 退出条件二选一：
//  1. r.Context() 被取消（浏览器关页面 / EventSource.close()）
//  2. 写失败（连接已断）——这时继续循环没意义
func (h *Handler) heartbeatLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("sse client disconnected", "reason", ctx.Err())
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				h.logger.Info("sse write failed, dropping client", "err", err)
				return
			}
			flusher.Flush()
		}
	}
}
