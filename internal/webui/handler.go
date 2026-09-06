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
//	GET  /           渲染首页（拉历史 + SSE 挂载点）
//	POST /messages   发消息（表单 body 字段）→ Client.SendMessage
//	GET  /events     SSE 长连接：订阅 Client 的 EventBus 并翻译成 SSE 帧
//
// 强约束（AGENTS.md §2）：前端只用 templ + HTMX，不引入 React/Vue
// 等框架，也不引入前端构建工具。htmx.min.js 与 SSE 扩展是 vendored
// 静态文件（见 assets.go），不走 npm / CDN。
package webui

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
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

// eventBuf 是 SSE 订阅的 EventBus 缓冲。
//
// EventBus 的契约是"满了就丢、发送端不阻塞"（见 core.EventBus），
// 给足缓冲降低高吞吐时丢消息的概率；取值与 pkg/tui 的 eventBuf 一致。
const eventBuf = 128

// DefaultConversationID 是 M4 单会话阶段使用的会话 ID，与 pkg/tui 一致。
// Hub 侧按 ConversationID 分桶存放历史，会话无需预先注册（见 pkg/tui 同名常量的注释）。
const DefaultConversationID = "lobby"

// historyLimit 是首页首次渲染时拉取的历史条数。
//
// 只影响 GET / 的首屏；翻页 / 加载更多是 M4.5 的活（提案 §M4.5）。
const historyLimit = 50

// Client 是 Handler 依赖的出站/入站窄接口。
//
// 直接依赖 *client.Client 也能跑，但收窄到四个方法后：
//   - 单元测试塞一个 stub 即可，不必起 hub；
//   - 将来 hub 自动重连（M4.6）包一层装饰器时，Handler 零修改。
//
// 编译期断言保证 *client.Client 始终满足本接口。
type Client interface {
	SendMessage(ctx context.Context, convID, body string) error
	Subscribe(buf int) core.Subscription
	History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error)
	Done() <-chan struct{}
}

var _ Client = (*client.Client)(nil)

// Config 是 Handler 的构造入参。
type Config struct {
	User    string // 显示名（M4 临时：由 -user flag 给，M6 换成登录）
	Device  string // 设备标识（web-<random>，见提案 §7.1）
	ConvID  string // 会话 ID，空则回落 DefaultConversationID
	Version string // ldflags 注入，仅展示用
}

// Handler 持有 web 端所有 HTTP 路由与其依赖。
type Handler struct {
	cfg    Config
	cli    Client
	logger *logging.ComponentLogger
}

// NewHandler 构造 Handler。cli 必须已完成 Connect（见 session.go 的 DialClient）。
func NewHandler(cfg Config, cli Client) *Handler {
	if cfg.ConvID == "" {
		cfg.ConvID = DefaultConversationID
	}
	return &Handler{
		cfg:    cfg,
		cli:    cli,
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

// handleHome 渲染首页：拉最近 historyLimit 条历史填充消息列表。
//
// 历史拉取失败不阻断整页渲染：给 banner 提示，页面仍可发新消息
// （发出去的消息会经 hub 回环从 SSE 流里回来）。
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

	msgs, err := h.cli.History(r.Context(), h.cfg.ConvID, 0, historyLimit)
	if err != nil {
		h.logger.Error("load history failed", "conv", h.cfg.ConvID, "err", err)
	}
	views := make([]templates.MessageView, 0, len(msgs))
	for i := range msgs {
		views = append(views, h.newView(&msgs[i]))
	}

	data := templates.HomeData{
		Meta: templates.PageMeta{
			Title:   "lanchat web",
			User:    h.cfg.User,
			Device:  h.cfg.Device,
			Version: h.cfg.Version,
		},
		Messages:  views,
		Connected: h.connected(),
	}
	if err != nil {
		data.Error = "历史加载失败，显示可能不完整"
	}
	h.renderHome(w, r, data)
}

// newView 把协议消息转成视图模型。Self 以 SenderUser 与当前会话身份比对得出。
func (h *Handler) newView(m *protocol.StoredMessage) templates.MessageView {
	return templates.NewMessageView(m.ID, int64(m.ServerSeq), m.SenderUserID, m.Body, m.CreatedAt, m.SenderUserID == h.cfg.User)
}

// connected 非阻塞判断与 hub 的连接是否存活。
func (h *Handler) connected() bool {
	select {
	case <-h.cli.Done():
		return false
	default:
		return true
	}
}

// renderHome 统一渲染入口，供 handleHome 与将来可能的错误渲染复用。
func (h *Handler) renderHome(w http.ResponseWriter, r *http.Request, data templates.HomeData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Home(data).Render(r.Context(), w); err != nil {
		h.logger.Error("render home failed", "err", err)
	}
}

// handleMessages 接收浏览器发来的消息并转发给 hub。
//
// 校验 → Client.SendMessage → 204。消息的回显不在这里做：hub 广播
// FKDeliver 回环给发送方自己，SSE 流（/events）会把它推回 DOM。
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
	if strings.TrimSpace(body) == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	if err := h.cli.SendMessage(r.Context(), h.cfg.ConvID, body); err != nil {
		h.logger.Error("send message failed", "conv", h.cfg.ConvID, "err", err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}

	// 204 No Content：HTMX 收到后不做任何 DOM 替换，输入框由
	// hx-on::after-request="this.reset()" 清空（见 home.templ）。
	w.WriteHeader(http.StatusNoContent)
}

// handleEvents 是 SSE 长连接端点。
//
// 订阅 Client 的 EventBus，把 core.Event 翻译成 SSE 帧推给浏览器：
//   - EventMessage → `event: message`，data 是渲染好的 <li> HTML（htmx 追加进 #messages）
//   - EventState   → `event: state`，data 是 connected / disconnected
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

	// 先订阅、再写首帧：两步之间 bus 上不会有漏掉事件的窗口。
	sub := h.cli.Subscribe(eventBuf)
	defer func() { _ = sub.Close() }()

	// 先写一个注释行让浏览器立刻拿到响应头（否则要等第一个 ticker）
	if _, err := w.Write([]byte(": lanchat sse ready\n\n")); err != nil {
		h.logger.Warn("sse initial write failed", "err", err)
		return
	}
	flusher.Flush()

	h.eventLoop(r.Context(), w, flusher, sub)
}

// eventLoop 把 EventBus 事件翻译成 SSE 帧推给浏览器。
//
// 退出条件三选一：
//  1. ctx 取消（浏览器关页面 / EventSource.close()）
//  2. cli.Done() 关闭（与 hub 的连接断了）——EventSource 会自动重连本端点，
//     重连后 handleHome 重新拉历史兜住断线期间的消息；hub 侧自动重连
//     是 M4.6 的活（提案 §M4.5）
//  3. 写失败（连接已断）——这时继续循环没意义
func (h *Handler) eventLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sub core.Subscription) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("sse client disconnected", "reason", ctx.Err())
			return
		case <-h.cli.Done():
			h.logger.Info("hub connection lost, closing sse stream")
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				h.logger.Info("sse write failed, dropping client", "err", err)
				return
			}
			flusher.Flush()
		case e := <-sub.C():
			frame, ok := h.sseFrame(ctx, e)
			if !ok {
				continue
			}
			if _, err := w.Write(frame); err != nil {
				h.logger.Info("sse write failed, dropping client", "err", err)
				return
			}
			flusher.Flush()
		}
	}
}

// sseFrame 把一条 core.Event 翻译成完整 SSE 帧；ok=false 表示该事件不产生帧。
//
// message 帧的 data 是渲染好的 <li> HTML 片段（templates.Message），按 SSE
// 规范逐行加 "data: " 前缀——templ 产物含换行（用户消息体可含换行），
// 整块塞一行会断帧；浏览器会把多个 data 行用 \n 拼回原文。
// id 行只在 ServerSeq > 0 时输出：EventSource 重连会带 Last-Event-ID 头，
// M4.6 可用它做断线补发。
func (h *Handler) sseFrame(ctx context.Context, e core.Event) ([]byte, bool) {
	switch e.Kind {
	case core.EventMessage:
		m := e.Message
		if m == nil || m.ConversationID != h.cfg.ConvID {
			return nil, false
		}
		var buf bytes.Buffer
		if err := templates.Message(h.newView(m)).Render(ctx, &buf); err != nil {
			h.logger.Error("render message frame failed", "err", err)
			return nil, false
		}
		var sb strings.Builder
		sb.WriteString("event: message\n")
		if m.ServerSeq > 0 {
			sb.WriteString("id: ")
			sb.WriteString(strconv.FormatUint(m.ServerSeq, 10))
			sb.WriteString("\n")
		}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			sb.WriteString("data: ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		return []byte(sb.String()), true

	case core.EventState:
		state := "disconnected"
		if e.State != nil && e.State.Connected {
			state = "connected"
		}
		return []byte("event: state\ndata: " + state + "\n\n"), true

	default:
		// EventRead / EventPresence / EventTyping：M4.3 不渲染。
		return nil, false
	}
}
