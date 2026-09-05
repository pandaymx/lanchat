package tui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// Config 是 Model 的依赖注入。
type Config struct {
	User    string
	Device  string
	HubURL  string
	MaxHist int // <=0 走默认 5000
}

// Model 是 TUI 的核心状态。
//
// 设计要点（见 doc.go）：
//   - Model 不持有 *Client.Client；所有外部事件经 inbox 通道投递给 Update。
//   - 所有状态写入都在 Update 所在 goroutine 内，无锁。
//   - 副作用通过 tea.Cmd 表达，副作用结果通过 inbox 回到 Update。
type Model struct {
	user, device, hubURL string
	maxHist              int
	inbox                chan tea.Msg

	// UI 状态
	width, height int
	ready         bool
	input         textInput
	history       historyView

	// 业务状态
	connected     bool
	lastError     error
	messages      []protocol.StoredMessage
	peers         []protocol.Presence
	lastSubmitted string
}

// New 构造一个未连接、待 Init 的 Model。
func New(cfg Config) *Model {
	if cfg.MaxHist <= 0 {
		cfg.MaxHist = 5000
	}
	return &Model{
		user:    cfg.User,
		device:  cfg.Device,
		hubURL:  cfg.HubURL,
		maxHist: cfg.MaxHist,
		inbox:   make(chan tea.Msg, 64),
		input:   newTextInput(),
		history: newHistoryView(),
	}
}

// Init 启动 inbox → Update 的循环。
func (m *Model) Init() tea.Cmd {
	return listenCmd(m.inbox)
}

// listenCmd 从 inbox 读一条 Msg 包成 tea.Cmd。
// bubbletea 调度该 Cmd 后把返回值传给 Update；Update 处理完后
// 再返回一个新的 listenCmd 维持循环，直到 quitMsg / tea.Quit 触发退出。
func listenCmd(inbox <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-inbox
		if !ok {
			return newQuitMsg("inbox closed")
		}
		return msg
	}
}

// Update 处理键盘 / WindowSizeMsg / 外部事件 / 错误事件 / 退出事件 / 提交事件。
// 所有状态写入都在这一处 goroutine，无锁。
//
// 键位路由优先级（与方案 §10 一致）：
//  1. Ctrl+C         → Quit
//  2. Enter 无 Shift → 取值、清空、投递 submitMsg（让 client adapter 在
//     eventMsg-style 路径上送到 hub；M3.5+ 实现）
//     Enter 带 Shift → 落到分支 3，由 textarea 自己处理换行
//  3. 其他键         → 转发给 textInput.Update
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		_, sidebarW, bodyH := layoutDims(msg.Width, msg.Height)
		m.history.SetSize(msg.Width-sidebarW, bodyH)
		m.input.SetSize(msg.Width, inputH)
		m.refreshHistory()
		return m, nil

	case tea.KeyMsg:
		k := msg.Key()
		// Ctrl+C 主动退出。
		if k.Code == 'c' && k.Mod == tea.ModCtrl {
			return m, tea.Cmd(tea.Quit)
		}
		// Enter（不带 Shift）→ 提交。Shift+Enter 落到 textInput 自己换行。
		if k.Code == tea.KeyEnter && k.Mod&tea.ModShift == 0 {
			return m, m.trySubmitInput()
		}
		// 其他键自动 focus textInput 后透传过去——首次按键即激活输入框。
		var cmds []tea.Cmd
		if !m.input.Focused() {
			cmds = append(cmds, m.input.Focus())
		}
		cmds = append(cmds, m.input.Update(msg))
		return m, tea.Batch(cmds...)

	case eventMsg:
		m.applyEvent(msg.event)
		return m, listenCmd(m.inbox)

	case errMsg:
		m.lastError = msg.err
		return m, listenCmd(m.inbox)

	case quitMsg:
		_ = msg.reason
		return m, tea.Cmd(tea.Quit)

	case submitMsg:
		// M3.5+ 由 client adapter 在事件循环里把这里收到的文本调 core.Send；
		// 当前阶段仅记录到 lastSubmitted，便于测试断言。
		m.lastSubmitted = msg.text
		return m, listenCmd(m.inbox)
	}

	return m, nil
}

// applyEvent 把 core.Event 反映到 Model 状态。
func (m *Model) applyEvent(e core.Event) {
	switch e.Kind {
	case core.EventState:
		if e.State != nil {
			m.connected = e.State.Connected
		}
	case core.EventMessage:
		if e.Message != nil {
			m.appendMessage(*e.Message)
			m.refreshHistory()
		}
	case core.EventPresence:
		if e.Presence != nil {
			m.upsertPresence(*e.Presence)
		}
	}
}

// refreshHistory 把当前 m.messages 推给 historyView，强制滚到底部。
// M3.3 阶段不区分「用户在中间看历史 → 不强拉」策略，每条新消息都强制 Tail。
// M3.4+ 引入未读计数时切换为 autoTailOnly=true 并做 unread 标记。
func (m *Model) refreshHistory() {
	m.history.SetMessages(m.messages, false)
}

// trySubmitInput 把当前 textInput 内容投递到 inbox，清空输入框。
//
// 三类 Noop：
//   - 空白：返回 nil cmd，不投递
//   - inbox 已满：仍写 lastSubmitted（保证观测），但丢掉消息并发 quit 提示
//   - 正常：先写入 lastSubmitted 让测试可读，再投递 submitMsg 维持 Update 协议
//
// 路由：submitMsg → Update → M3.5+ 的 client adapter 收到并 Send。
func (m *Model) trySubmitInput() tea.Cmd {
	text := strings.TrimRight(m.input.Value(), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	m.input.Reset()
	m.lastSubmitted = text
	select {
	case m.inbox <- newSubmitMsg(text):
	default:
		m.PublishError(errInboxFull)
	}
	return listenCmd(m.inbox)
}

// errInboxFull 描述 inbox 通道已满、submitMsg 被丢弃的情况。
// 定义在 model.go 顶层，Test 也能 import 引用。
var errInboxFull = errors.New("tui: inbox is full, submit dropped")

// appendMessage 把消息加入历史；超限时丢弃最早的批（保留 10% headroom
// 避免每条新消息都触发一次 slice copy）。
func (m *Model) appendMessage(msg protocol.StoredMessage) {
	m.messages = append(m.messages, msg)
	// 触发裁剪的上限 = maxHist + maxHist/10；裁剪后保留 maxHist 条。
	const headroomDiv = 10
	upper := m.maxHist + m.maxHist/headroomDiv
	if len(m.messages) > upper {
		drop := len(m.messages) - m.maxHist
		if drop > len(m.messages) {
			drop = len(m.messages)
		}
		m.messages = append([]protocol.StoredMessage(nil), m.messages[drop:]...)
	}
}

// upsertPresence 按 DeviceID upsert 在线设备。
func (m *Model) upsertPresence(p protocol.Presence) {
	for i := range m.peers {
		if m.peers[i].DeviceID == p.DeviceID {
			m.peers[i] = p
			return
		}
	}
	m.peers = append(m.peers, p)
}

// Publish 把外部事件投递到 inbox。M3.5+ 由 client bus → adapter 调用。
// inbox 满时静默丢弃非关键状态；调用方不能依赖 Publish 同步返回。
func (m *Model) Publish(e core.Event) {
	select {
	case m.inbox <- newEventMsg(e):
	default:
	}
}

// PublishError 把非致命错误投递到 inbox。
func (m *Model) PublishError(err error) {
	select {
	case m.inbox <- newErrMsg(err):
	default:
	}
}

// RequestQuit 请求退出，reason 写入 lastError 方便诊断。
func (m *Model) RequestQuit(reason string) {
	select {
	case m.inbox <- newQuitMsg(reason):
	default:
	}
}

// View 返回当前帧的四区拼装结果。
// M3.3 已替换占位文字 → status / (history+sidebar) / input 三段纵向布局。
func (m *Model) View() tea.View {
	status := m.renderStatus()
	historyView := m.history.View()
	sidebarView := m.renderSidebar()
	inputView := m.input.View()

	body := renderLayout(m.width, m.height, status, historyView, sidebarView, inputView)
	return tea.NewView(body)
}

// renderStatus 生成状态栏字符串：连接状态 + 用户/设备 + hub URL。
// M3.3 是纯文本；M3.4+ 接 Wireshark 风格末尾再加 latency/last_seq。
func (m *Model) renderStatus() string {
	conn := "offline"
	if m.connected {
		conn = "online"
	}
	parts := []string{
		conn,
		"user=" + m.user,
		"device=" + m.device,
		"hub=" + m.hubURL,
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " | "
		}
		out += p
	}
	if m.lastError != nil {
		out += " | err=" + m.lastError.Error()
	}
	return out
}

// renderSidebar 生成右侧在线设备列表文本。
func (m *Model) renderSidebar() string {
	if len(m.peers) == 0 {
		return "peers: (none yet)"
	}
	online := 0
	for _, p := range m.peers {
		if p.Online {
			online++
		}
	}
	out := "peers: " + itoa(online) + "/" + itoa(len(m.peers)) + "\n"
	for _, p := range m.peers {
		if !p.Online {
			continue
		}
		out += "● " + p.UserID + "@" + p.DeviceID + "\n"
	}
	return out
}

// itoa 是 strconv.Itoa 的极简替代，避免在 renderSidebar 顶层 import strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// User returns the user identity bound at construction.
func (m *Model) User() string { return m.user }

// Device returns the per-device identifier bound at construction.
func (m *Model) Device() string { return m.device }

// HubURL returns the hub endpoint configured at construction.
func (m *Model) HubURL() string { return m.hubURL }

// Connected reports whether the last applied state event marked the link up.
func (m *Model) Connected() bool { return m.connected }

// LastError returns the most recent non-fatal error delivered via errMsg.
func (m *Model) LastError() error { return m.lastError }

// PeerCount returns the number of currently tracked presence records.
func (m *Model) PeerCount() int { return len(m.peers) }

// UniqueUserCount returns the number of distinct users across all peers.
func (m *Model) UniqueUserCount() int { return uniqueUsers(m.peers) }

// Messages returns a copy of the in-memory history slice for read-only callers.
func (m *Model) Messages() []protocol.StoredMessage {
	out := make([]protocol.StoredMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

// Width returns the terminal width captured from the last WindowSizeMsg.
func (m *Model) Width() int { return m.width }

// Height returns the terminal height captured from the last WindowSizeMsg.
func (m *Model) Height() int { return m.height }

// Ready reports whether at least one WindowSizeMsg has been observed.
func (m *Model) Ready() bool { return m.ready }

// InboxLen 返回 inbox 当前缓冲长度，方便测试断言。
func (m *Model) InboxLen() int { return len(m.inbox) }

// LastSubmitted 返回最近一次用户通过 Enter 提交的文本。
// 当前阶段（无 client adapter）仅作测试钩子，M3.5+ 由 client adapter
// 在事件循环里读到 submitMsg 并调 core.Send 后，这里仍保留原值。
func (m *Model) LastSubmitted() string { return m.lastSubmitted }

// FocusInput 把光标放回输入框；外部 program 启动时调用一次。
func (m *Model) FocusInput() tea.Cmd {
	return m.input.Focus()
}

// uniqueUsers 计算去重后的在线用户数。
func uniqueUsers(peers []protocol.Presence) int {
	seen := make(map[string]struct{}, len(peers))
	for _, p := range peers {
		seen[p.UserID] = struct{}{}
	}
	return len(seen)
}

// 编译期断言：Model 满足 tea.Model。
var _ tea.Model = (*Model)(nil)
