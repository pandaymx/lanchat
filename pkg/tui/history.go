package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// historyView 封装 viewport.Model，提供消息流的只读视图。
//
// 职责：把 Model.messages 序列化为纯字符串并交给 viewport 渲染；
// 不参与任何事件路由（viewport 自己消费 PageUp/PageDown/Up/Down 等）。
//
// M3.8.2 起内部镜像一份 []string 缓冲，让 AppendMessage 单条增量追加
// 走 SetContentLines 而非 SetContent，避免每次新消息都全量 split+join。
type historyView struct {
	inner      viewport.Model
	lines      []string
	translator Translator // M3.10：本地化接口

	// readMark 是「某条消息是否已被其它设备读到」的判定（M8.1），由
	// Model 注入（读它的 reads 快照）；nil 时不渲染已读标记。只在
	// Update goroutine 内被 formatMessage 调用，无需加锁。
	readMark func(senderUserID string, seq uint64) bool

	// savedPath 返回「已下载到本地」的文件消息路径（M9），由 Model 注入
	// （读它的 fileSaved 快照）；nil 时附件卡片不带 saved 标记。与
	// readMark 同一模式，只在 Update goroutine 内被 formatMessage 调用。
	savedPath func(id string) (string, bool)

	// mdCache 是 Markdown 渲染结果缓存（M8.2），按消息 ID 键控。
	// 消息体不可变，而同一消息会因新消息到达 / 已读标记变化 / 窗口
	// resize 被反复 formatMessage；glamour 渲染每条约几十 µs，缓存后
	// 这些路径都是 O(1) 查表。总量受消息上限约束，不会无限增长。
	mdCache map[string]string
}

// 历史消息行最大宽度；超过则折叠显示，避免撑爆窄终端。
const messageLineWrap = 200

// newHistoryView 用合理默认值构造一个 historyView。
//
// 初始 width/height 是占位，会被 WindowSizeMsg 触发的 SetSize 覆盖。
//
// M3.10：tr 为 nil 时用 nopTranslator fallback。
func newHistoryView(tr Translator) historyView {
	vp := viewport.New(
		viewport.WithWidth(40),
		viewport.WithHeight(10),
	)
	if tr == nil {
		tr = nopTranslator{}
	}
	return historyView{
		inner:      vp,
		lines:      make([]string, 0, 256),
		translator: tr,
		mdCache:    make(map[string]string),
	}
}

// t 是 historyView 内部 helper，转发到 translator；M3.10 加。
func (h *historyView) t(key string) string {
	return h.translator.T(key)
}

// Init 返回 viewport 自己的初始化 Cmd。
func (h *historyView) Init() tea.Cmd {
	return h.inner.Init()
}

// Update 把消息透传给底层 viewport；M3.4+ 还会扩展为拦截滚轮等。
func (h *historyView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	h.inner, cmd = h.inner.Update(msg)
	return cmd
}

// View 返回 viewport 当前字符串画面。
func (h *historyView) View() string {
	return h.inner.View()
}

// SetSize 调整 width/height，outer Model 在 WindowSizeMsg 时调用。
func (h *historyView) SetSize(w, height int) {
	h.inner.SetWidth(w)
	h.inner.SetHeight(height)
}

// SetMessages 把 msgs 序列化为字符串并写入 viewport。
//
// 与 M3.3 时期不同：M3.6 不再自动 GotoBottom。
// 调用方（M3.6 的 refreshHistory）负责根据 AtBottom 决定是否拉回底部：
//   - 用户在底部 → 跟随新消息，调用方再调一次 GotoBottom()
//   - 用户在中段/顶部 → 保持当前 yoffset（不打断阅读），未读计数累加
//
// 同时不再接受 autoTailOnly 开关——AtBottom 状态是 viewport 自描述的，
// 不需要外部再传一个冗余信号。这把 SetMessages 收敛成「纯内容替换」。
//
// M3.8.2 起内部维护 lines 镜像缓冲，避免每条新消息都全量 split+join。
func (h *historyView) SetMessages(msgs []protocol.StoredMessage) {
	h.lines = h.lines[:0]
	for i := range msgs {
		h.lines = append(h.lines, formatMessage(h, msgs[i]))
	}
	h.applyLines()
}

// AppendMessage 单条增量追加到 viewport 内容。
//
// M3.8.2 引入：消息高频到达（每秒数十条）时，避免每条都走全量 SetMessages
// + SetContent 重新 split+join。SetContentLines 在 lines 不含 \r\n 的情况下
// 走快速路径（O(n) 扫描但常数项比 split 小）。
//
// 调用方（M3.8.2 的 Model.appendMessage）按需触发，wasAtBottom 锚定的
// 行为由 Model.applyEvent 负责；本方法只管内容追加。
func (h *historyView) AppendMessage(msg protocol.StoredMessage) {
	h.lines = append(h.lines, formatMessage(h, msg))
	h.applyLines()
}

// PrependMessages 把更早的消息渲染后插到内容最前（M7.1 上翻分页），
// 并保持视觉锚点：原视口首行在插入后下移 N 行，YOffset 同步补偿 N，
// 用户不会因为加载而跳屏。返回实际新增的行数。
func (h *historyView) PrependMessages(msgs []protocol.StoredMessage) int {
	if len(msgs) == 0 {
		return 0
	}
	prevOffset := h.inner.YOffset()
	newLines := make([]string, 0, len(msgs))
	for i := range msgs {
		newLines = append(newLines, formatMessage(h, msgs[i]))
	}
	h.lines = append(newLines, h.lines...)
	h.applyLines()
	h.inner.SetYOffset(prevOffset + len(newLines))
	return len(newLines)
}

// applyLines 把 h.lines 镜像给底层 viewport，统一 SetContentLines 的入口。
func (h *historyView) applyLines() {
	h.inner.SetContentLines(h.lines)
}

// ScrollUp 把视口上滚 n 行；n<=0 时无操作。M3.6 给 Model 提供显式 API，
// 不依赖底层 textarea 的键位，因为 textarea 默认会拦截 Up/Down。
func (h *historyView) ScrollUp(n int) {
	if n <= 0 {
		return
	}
	h.inner.ScrollUp(n)
}

// ScrollDown 把视口下滚 n 行；若超过内容下界则停在底部。
func (h *historyView) ScrollDown(n int) {
	if n <= 0 {
		return
	}
	h.inner.ScrollDown(n)
}

// PageUp 整页上滚。等价于 ScrollUp(Height())。
func (h *historyView) PageUp() {
	h.inner.PageUp()
}

// PageDown 整页下滚。等价于 ScrollDown(Height())。
func (h *historyView) PageDown() {
	h.inner.PageDown()
}

// GotoBottom 滚到最底；用户「我要看新消息」的明确动作。
func (h *historyView) GotoBottom() {
	h.inner.GotoBottom()
}

// Width 返回当前视口宽度，供 Model 估算布局使用。
func (h *historyView) Width() int { return h.inner.Width() }

// Height 返回当前视口高度，主要给测试断言用。
func (h *historyView) Height() int { return h.inner.Height() }

// AtBottom 报告视口是否已滚到底部，给 M3.6 的 unread 计数判断用：
// 新消息到达时若用户在底部就跟着滚下去；否则只累加 unread。
func (h *historyView) AtBottom() bool { return h.inner.AtBottom() }

// AtTop 报告视口是否已滚到顶部，M7.1 上翻分页的触发条件。
func (h *historyView) AtTop() bool { return h.inner.AtTop() }

// SetReadChecker 注入「某条消息是否已被他人读到」的判定（M8.1）。
// fn 为 nil 时 formatMessage 不渲染已读标记（旧测试/无状态场景兼容）。
func (h *historyView) SetReadChecker(fn func(senderUserID string, seq uint64) bool) {
	h.readMark = fn
}

// SetSavedPathChecker 注入「消息已下载到本地路径」查询（M9）。
// 渲染附件卡片时据此追加 saved 状态；nil（未注入）则只显示文件行。
func (h *historyView) SetSavedPathChecker(fn func(id string) (string, bool)) {
	h.savedPath = fn
}

// formatMessage 把 StoredMessage 渲染为单行文本。
//
// 格式：`[HH:MM:SS] user: body`。M8.2 起 body 经 mdBody 走 glamour
// Markdown 渲染（加粗/行内代码/代码块高亮）；纯文本消息渲染结果与
// 旧版逐字一致（glamour 对无格式段落不加装饰）。
//
// M3.10：fallback 字符（"?"、"??:??:??"）走 Translator；en/zh-CN 都用
// 同一字面字符（不需要翻译），但留出 hook 以备未来扩展。
func formatMessage(h *historyView, m protocol.StoredMessage) string {
	who := m.SenderUserID
	if who == "" {
		who = m.SenderDeviceID
	}
	if who == "" {
		who = h.t("tui.history.fallback.user")
	}
	ts := formatUnixMilli(m.CreatedAt, h)
	var line string
	// M9：附件消息——正文让位给文件卡片行（TUI /file 发的消息 body 恒空；
	// web 端带 caption 时正文在卡片下方，这里以文件行为准）。
	if m.File != nil {
		card := fmt.Sprintf("%s %s (%s)",
			h.t("tui.file.tag"), m.File.Name, formatSize(m.File.Size))
		if p, ok := h.savedPath(m.ID); ok {
			card += " " + savedStyle.Render(fmt.Sprintf(h.t("tui.file.saved"), p))
		}
		line = fmt.Sprintf("[%s] %s: %s", ts, who, card)
	} else {
		line = fmt.Sprintf("[%s] %s: %s", ts, who, h.mdBody(m))
	}
	// M8.1：自己发的消息被其它设备读到后追加「✓已读」标记。
	if h.readMark != nil && h.readMark(m.SenderUserID, m.ServerSeq) {
		line += " " + readStyle.Render(h.t("tui.history.read"))
	}
	return line
}

// formatSize 把字节数格式化成人读尺寸（B/KB/MB/GB，1024 进制）。
func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// mdBody 渲染消息体 Markdown（M8.2），结果按消息 ID 缓存。
//
// ID 为空（本地预显消息等场景）不缓存，直接渲染。
// 渲染前先 truncateBody 截断：截断点不会把 ANSI 码算进长度，
// 与旧版「先截断再展示」的边界保持一致。
func (h *historyView) mdBody(m protocol.StoredMessage) string {
	body := truncateBody(m.Body)
	if m.ID == "" {
		return mdRender(body)
	}
	if h.mdCache == nil {
		h.mdCache = make(map[string]string)
	}
	if s, ok := h.mdCache[m.ID]; ok {
		return s
	}
	s := mdRender(body)
	h.mdCache[m.ID] = s
	return s
}

// truncateBody 把超长消息体截断到 messageLineWrap（保留旧行为：
// 先截断再渲染）。
func truncateBody(body string) string {
	if len(body) > messageLineWrap {
		return body[:messageLineWrap] + "..."
	}
	return body
}

// formatUnixMilli 把 Unix 毫秒格式化为 HH:MM:SS；零值（未设置）返回 "??:??:??"。
//
// M3.10：fallback 走 Translator。
func formatUnixMilli(ms int64, h *historyView) string {
	if ms == 0 {
		return h.t("tui.history.fallback.time")
	}
	return time.UnixMilli(ms).Format("15:04:05")
}
