package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// historyView 封装 viewport.Model，提供消息流的只读视图。
//
// 职责：把 Model.messages 序列化为纯字符串并交给 viewport 渲染；
// 不参与任何事件路由（viewport 自己消费 PageUp/PageDown/Up/Down 等）。
type historyView struct {
	inner viewport.Model
}

// 历史消息行最大宽度；超过则折叠显示，避免撑爆窄终端。
const messageLineWrap = 200

// newHistoryView 用合理默认值构造一个 historyView。
//
// 初始 width/height 是占位，会被 WindowSizeMsg 触发的 SetSize 覆盖。
func newHistoryView() historyView {
	vp := viewport.New(
		viewport.WithWidth(40),
		viewport.WithHeight(10),
	)
	return historyView{inner: vp}
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
func (h *historyView) SetMessages(msgs []protocol.StoredMessage) {
	if len(msgs) == 0 {
		h.inner.SetContent("")
		return
	}
	lines := make([]string, 0, len(msgs))
	for i := range msgs {
		lines = append(lines, formatMessage(msgs[i]))
	}
	h.inner.SetContent(strings.Join(lines, "\n"))
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

// formatMessage 把 StoredMessage 渲染为单行文本。
//
// 格式：`[HH:MM:SS] user: body`；M4+ 计划叠加 Markdown 渲染与发送者颜色。
// 当前阶段重在排版骨架稳定，色彩/高亮留到 M3.4 之后。
func formatMessage(m protocol.StoredMessage) string {
	who := m.SenderUserID
	if who == "" {
		who = m.SenderDeviceID
	}
	if who == "" {
		who = "?"
	}
	ts := formatUnixMilli(m.CreatedAt)
	body := m.Body
	if len(body) > messageLineWrap {
		body = body[:messageLineWrap] + "..."
	}
	return fmt.Sprintf("[%s] %s: %s", ts, who, body)
}

// formatUnixMilli 把 Unix 毫秒格式化为 HH:MM:SS；零值（未设置）返回 "??:??:??"。
func formatUnixMilli(ms int64) string {
	if ms == 0 {
		return "??:??:??"
	}
	return time.UnixMilli(ms).Format("15:04:05")
}
