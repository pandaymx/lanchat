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
//	POST /messages   发消息（表单 body 字段）→ Session.SendMessage
//	GET  /events     SSE 长连接：注册到 Session fanout，读帧推浏览器
//
// 会话模型（M4.4）：一枚 lanchat_session cookie 对应一份 Session（一条
// 到 hub 的 client 连接），多 tab 共享 cookie 即共享 Session；Session 的
// pump goroutine 把 EventBus 事件渲染一次、fanout 给所有 SSE 连接。
//
// 强约束（AGENTS.md §2）：前端只用 templ + HTMX，不引入 React/Vue
// 等框架，也不引入前端构建工具。htmx.min.js 与 SSE 扩展是 vendored
// 静态文件（见 assets.go），不走 npm / CDN。
package webui

import (
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

// eventBuf 是 Session pump 订阅 EventBus 的缓冲。
//
// EventBus 的契约是"满了就丢、发送端不阻塞"（见 pkg/event），
// 给足缓冲降低高吞吐时丢消息的概率；取值与 pkg/tui 的 eventBuf 一致。
const eventBuf = 128

// DefaultConversationID 是 M4 单会话阶段使用的会话 ID，与 pkg/tui 一致。
// Hub 侧按 ConversationID 分桶存放历史，会话无需预先注册（见 pkg/tui 同名常量的注释）。
const DefaultConversationID = "lobby"

// historyLimit 是首页首次渲染时拉取的历史条数。
//
// 只影响 GET / 的首屏；翻页 / 加载更多是 M4.5 的活（提案 §M4.5）。
const historyLimit = 50

// Client 是 webui 依赖的出站/入站窄接口。
//
// 直接依赖 *client.Client 也能跑，但收窄到五个方法后：
//   - 单元测试塞一个 stub 即可，不必起 hub；
//   - 将来 hub 自动重连（M4.6）包一层装饰器时，Handler 零修改。
//
// 编译期断言保证 *client.Client 始终满足本接口。
type Client interface {
	SendMessage(ctx context.Context, convID, body string) error
	Subscribe(buf int) core.Subscription
	History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error)
	// FetchHistory 向 hub 同步拉取一段历史（before>0 向更早翻页）。
	// 结果已写入 client 本地 Store，但不发布 EventMessage——分页片段
	// 由本端点直接渲染返回，避免经 SSE 通道重复追加。
	FetchHistory(ctx context.Context, convID string, after, before uint64, limit int) (protocol.HistoryResponse, error)
	// Peers 返回当前在线成员名单快照（M7.2）。presence 不持久化、不随
	// EventBus 重放，首屏成员列表只能从 Client 的连接级状态取；SSE 推送
	// 的 presence 帧也以它为数据源渲染全量列表。
	Peers() []protocol.Presence
	// Typing 返回当前正在输入的成员快照（M7.3），typing SSE 帧的数据源。
	Typing() []protocol.Typing
	// SendTyping 上发「正在输入」提示（M7.3）；POST /typing 的出站路径。
	SendTyping(ctx context.Context) error
	Done() <-chan struct{}
	// Close 释放底层连接（Manager 回收 Session 时调，顺序先于 store.Close）。
	Close() error
}

var _ Client = (*client.Client)(nil)

// Config 是 Handler 的构造入参。身份字段（User/Device/ConvID）在
// Manager / Session 上——多 session 场景下 Handler 不持有单一身份。
type Config struct {
	Version string // ldflags 注入，仅展示用
	// Translator 是 UI chrome 文案翻译器（M4.7）；nil 时模板兜底返回
	// key 字面值（见 templates.T）。cmd/web 装配期注入 i18n bundle。
	Translator templates.Translator
}

// Handler 持有 web 端所有 HTTP 路由与其依赖。
type Handler struct {
	cfg    Config
	mgr    *Manager
	logger *logging.ComponentLogger
}

// NewHandler 构造 Handler。Session 由 mgr 按 cookie 惰性拨号创建。
func NewHandler(cfg Config, mgr *Manager) *Handler {
	return &Handler{
		cfg:    cfg,
		mgr:    mgr,
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
	mux.HandleFunc("/typing", h.handleTyping)
	mux.HandleFunc("/history", h.handleHistory)
	mux.HandleFunc("/events", h.handleEvents)
	mux.Handle("/assets/", http.StripPrefix("/assets/", StaticHandler()))
}

// handleTyping 是「正在输入」端点（M7.3）：
//
//   - POST：浏览器 input 节流上发，透传给 client.SendTyping（hub 盖戳广播）；
//   - GET：typing 指示条自刷新用——片段渲染后 6s 自动 GET 一次，停止输入
//     后 client 快照惰性过期为空，返回的空片段把指示条清掉并停止轮询。
func (h *Handler) handleTyping(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		sess, err := h.ensureSession(w, r)
		if err != nil {
			h.logger.Error("typing: session unavailable", "err", err)
			http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := sess.cli.SendTyping(r.Context()); err != nil {
			h.logger.Debug("send typing failed", "err", err)
			http.Error(w, "typing failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		// 自刷新只读快照：拨号失败/无会话都渲染空片段（清掉指示条）。
		views := []templates.TypingView(nil)
		if sess, err := h.ensureSession(w, r); err == nil {
			views = templates.NewTypingViews(sess.cli.Typing())
		}
		h.renderTyping(w, r, views)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderTyping 渲染 typing 指示条片段（GET /typing 与 SSE typing 帧共用）。
func (h *Handler) renderTyping(w http.ResponseWriter, r *http.Request, views []templates.TypingView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var tr templates.Translator
	if h.cfg.Translator != nil {
		tr = h.cfg.Translator
	}
	if err := templates.TypingBar(tr, views).Render(r.Context(), w); err != nil {
		h.logger.Error("render typing bar failed", "err", err)
	}
}

// ensureSession 取当前请求的 Session：无 cookie 则当场签发并惰性拨号。
//
// 拨号失败（hub 不在线）返回错误，调用方回 503——浏览器重试 / EventSource
// 自动重连即可，web server 本身照常跑（hub 恢复后下一个请求自愈）。
func (h *Handler) ensureSession(w http.ResponseWriter, r *http.Request) (*Session, error) {
	id := readSessionID(r)
	if id == "" {
		id = newSessionID()
		issueSessionCookie(w, id)
	}
	sess, err := h.mgr.GetOrCreate(r.Context(), id)
	if err != nil {
		return nil, err
	}
	sess.touch()
	return sess, nil
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

	sess, err := h.ensureSession(w, r)
	if err != nil {
		h.logger.Error("home: session unavailable", "err", err)
		http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
		return
	}

	msgs, histErr := sess.cli.History(r.Context(), sess.convID, 0, historyLimit)
	if histErr != nil {
		h.logger.Error("load history failed", "conv", sess.convID, "err", histErr)
	}
	views := make([]templates.MessageView, 0, len(msgs))
	for i := range msgs {
		views = append(views, sess.newView(&msgs[i]))
	}

	data := templates.HomeData{
		Meta: templates.PageMeta{
			Title:   "lanchat web",
			User:    sess.user,
			Device:  sess.device,
			Version: h.cfg.Version,
		},
		Messages:  views,
		Peers:     templates.NewPeerViews(sess.cli.Peers(), sess.device),
		Connected: sess.alive(),
		Tr:        h.cfg.Translator,
		// 首屏拉满 limit 即认为可能还有更早的消息（store 是内存视图，
		// 无法直接区分"正好 50 条"与"还有更多"；点一次加载更多便知分晓）。
		HasMore: histErr == nil && len(msgs) == historyLimit,
	}
	if data.HasMore {
		data.OldestSeq = int64(msgs[0].ServerSeq)
	}
	if histErr != nil {
		data.Error = templates.T(h.cfg.Translator, "web.history.error")
	}
	h.renderHome(w, r, data)
}

// handleHistory 是「加载更早消息」的分页端点（M4.5）。
//
// 查询参数 before=<ServerSeq>：返回该序号之前的最晚 historyLimit 条
// （升序）。响应是 HistoryPage 片段——消息 <li> 序列 + 可能的新
// LoadMore 按钮，由 htmx outerHTML 替换被点击的按钮行。
func (h *Handler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	before, err := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	if err != nil || before == 0 {
		http.Error(w, "before query param must be a positive seq", http.StatusBadRequest)
		return
	}

	sess, err := h.ensureSession(w, r)
	if err != nil {
		h.logger.Error("history: session unavailable", "err", err)
		http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
		return
	}

	resp, err := sess.cli.FetchHistory(r.Context(), sess.convID, 0, before, historyLimit)
	if err != nil {
		h.logger.Error("fetch history failed", "conv", sess.convID, "before", before, "err", err)
		http.Error(w, "history unavailable", http.StatusServiceUnavailable)
		return
	}

	views := make([]templates.MessageView, 0, len(resp.Messages))
	for i := range resp.Messages {
		views = append(views, sess.newView(&resp.Messages[i]))
	}
	// 下一页游标：本批最老一条的 seq（resp.Messages 升序，首条即最老）。
	var nextBefore int64
	if resp.HasMore && len(resp.Messages) > 0 {
		nextBefore = int64(resp.Messages[0].ServerSeq)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.HistoryPage(h.cfg.Translator, views, resp.HasMore, nextBefore).Render(r.Context(), w); err != nil {
		h.logger.Error("render history page failed", "err", err)
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
// 校验 → Session.SendMessage → 204。消息的回显不在这里做：hub 广播
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

	sess, err := h.ensureSession(w, r)
	if err != nil {
		h.logger.Error("messages: session unavailable", "err", err)
		http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := sess.cli.SendMessage(r.Context(), sess.convID, body); err != nil {
		h.logger.Error("send message failed", "conv", sess.convID, "err", err)
		http.Error(w, "send failed", http.StatusInternalServerError)
		return
	}

	// 204 No Content：HTMX 收到后不做任何 DOM 替换，输入框由
	// hx-on::after-request="this.reset()" 清空（见 home.templ）。
	w.WriteHeader(http.StatusNoContent)
}

// handleEvents 是 SSE 长连接端点。
//
// 连接注册到 Session 的 fanout（多 tab 共享同一条 client 连接）；
// pump goroutine 已把 core.Event 翻译成 SSE 帧，这里只负责：
//   - 写响应头与首帧注释（让浏览器立刻拿到响应头）
//   - 循环把 fanout 帧写给本连接、15s 心跳
//   - 退出时 defer 注销 writer
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

	sess, err := h.ensureSession(w, r)
	if err != nil {
		h.logger.Error("events: session unavailable", "err", err)
		http.Error(w, "hub unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 告诉中间代理这是长连接，别缓冲
	w.Header().Set("X-Accel-Buffering", "no")

	h.logger.Info("sse client connected", "remote", r.RemoteAddr, "cookie", sess.id)

	// 注册 writer 后立即写首帧：两步之间 pump 已在跑，事件不会丢
	// （writer ch 有 128 缓冲）。
	wr := sess.addWriter()
	defer sess.removeWriter(wr)
	sess.touch()

	if _, err := w.Write([]byte(": lanchat sse ready\n\n")); err != nil {
		h.logger.Warn("sse initial write failed", "err", err)
		return
	}
	flusher.Flush()

	// 断线重连补发：EventSource 自动把最后收到的 message 帧 id 放在
	// Last-Event-ID 头里。把缺口消息直接补写给本连接（幂等去重由
	// writer.skipSeq + 事件循环过滤保证，见 sse.go catchUp）。
	if lastID := parseLastEventID(r); lastID > 0 {
		sess.catchUp(w, flusher, wr, lastID)
	}

	h.serveSSE(r.Context(), w, flusher, sess, wr)
}

// parseLastEventID 读取 EventSource 重连自动携带的 Last-Event-ID 头。
// 缺失或非法（非正整数）返回 0——视为全新连接，不补发。
func parseLastEventID(r *http.Request) uint64 {
	id := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if id == "" {
		return 0
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// serveSSE 是单条 SSE 连接的写循环。
//
// 退出条件三选一：
//  1. ctx 取消（浏览器关页面 / EventSource.close()）
//  2. wr.ch 关闭（session 死亡：hub 断开或被 Manager 回收）——EventSource
//     会自动重连本端点，重连后 ensureSession 重建 session 并补历史
//  3. 写失败（连接已断）——这时继续循环没意义
func (h *Handler) serveSSE(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sess *Session, wr *sseWriter) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("sse client disconnected", "cookie", sess.id, "reason", ctx.Err())
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				h.logger.Info("sse write failed, dropping client", "cookie", sess.id, "err", err)
				return
			}
			flusher.Flush()
		case chunk, ok := <-wr.ch:
			if !ok {
				// session 没了：结束流，浏览器自动重连。
				h.logger.Info("sse stream closed: session gone", "cookie", sess.id)
				return
			}
			// 重连补发已直接写过该序号（catchUp），fanout 缓冲里的
			// 同序号帧跳过，保证补发与实时帧幂等不重复。
			if chunk.seq > 0 && chunk.seq <= wr.skipSeq.Load() {
				continue
			}
			if _, err := w.Write(chunk.data); err != nil {
				h.logger.Info("sse write failed, dropping client", "cookie", sess.id, "err", err)
				return
			}
			flusher.Flush()
		}
	}
}
