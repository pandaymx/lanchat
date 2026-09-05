package tui

import (
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

	// 业务状态
	connected bool
	lastError error
	messages  []protocol.StoredMessage
	peers     []protocol.Presence
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

// Update 处理键盘 / WindowSizeMsg / 外部事件 / 错误事件 / 退出事件。
// 所有状态写入都在这一处 goroutine，无锁。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		k := msg.Key()
		// Ctrl+C 主动退出；M3.3+ 把可输入键路由到 textarea.Model。
		if k.Code == 'c' && k.Mod == tea.ModCtrl {
			return m, tea.Cmd(tea.Quit)
		}
		return m, nil

	case eventMsg:
		m.applyEvent(msg.event)
		return m, listenCmd(m.inbox)

	case errMsg:
		m.lastError = msg.err
		return m, listenCmd(m.inbox)

	case quitMsg:
		_ = msg.reason
		return m, tea.Cmd(tea.Quit)
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
		}
	case core.EventPresence:
		if e.Presence != nil {
			m.upsertPresence(*e.Presence)
		}
	}
}

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

// View 返回占位视图。M3.3 替换为 status / history / sidebar / textarea。
func (m *Model) View() tea.View {
	return tea.NewView("lanchat tui (m3.2 skeleton)")
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
