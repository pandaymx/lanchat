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

// SetMessages 替换内容为 msgs 的格式化结果，并自动滚到底部。
//
// autoTailOnly 决定如果用户已经手动向上滚动，是否还要把视口拉回底部：
// M3.3 暂传 false（仅在初始化时拉底），M3.4+ 再做「未读时不强拉」策略。
func (h *historyView) SetMessages(msgs []protocol.StoredMessage, autoTailOnly bool) {
	if len(msgs) == 0 {
		h.inner.SetContent("")
		h.inner.GotoBottom()
		return
	}
	lines := make([]string, 0, len(msgs))
	for i := range msgs {
		lines = append(lines, formatMessage(msgs[i]))
	}
	h.inner.SetContent(strings.Join(lines, "\n"))
	if autoTailOnly && !h.inner.AtBottom() {
		return
	}
	h.inner.GotoBottom()
}

// Width 返回当前视口宽度，供 Model 估算布局使用。
func (h *historyView) Width() int { return h.inner.Width() }

// Height 返回当前视口高度，主要给测试断言用。
func (h *historyView) Height() int { return h.inner.Height() }

// AtBottom 报告视口是否已滚到底部，给外面的 unread 计数逻辑用。
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
