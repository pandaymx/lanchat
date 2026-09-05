package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// tuiLog 是 pkg/tui 的 logger。包级单例，组件多不便注入。
var tuiLog = logging.New("tui")

// Config 是 Model 的依赖注入。
//
// Sender 可选：无 Sender 时 TUI 仍能跑（仅记 lastSubmitted 给测试/调试看），
// cmd/tui 接入 Session 后会注入；测试用例继续用 nil 即可。
type Config struct {
	User    string
	Device  string
	HubURL  string
	MaxHist int // <=0 走默认 5000

	// Sender 是出站消息的窄接口。Model.submitMsg 命中后会把
	// 文本交给 Sender.Send，再通过 sentMsg 续上 listenCmd。
	Sender Sender
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
	sender               Sender

	// UI 状态
	width, height int
	ready         bool
	input         textInput
	history       historyView

	// 业务状态
	connected     bool
	lastError     error
	errExpireAt   time.Time // M3.9.3: lastError 红字显示到期时间
	helpMode      bool      // M3.9.1: /help 切换
	messages      []protocol.StoredMessage
	peers         []protocol.Presence
	lastSubmitted string

	// M3.6：用户没有跟随底部时新消息的计数；MarkRead 时清零。
	unread int
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
		sender:  cfg.Sender,
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

// sendTimeout 是出站 Send 的硬性上限。
//
// bubbletea 的 Cmd 同步阻塞会让事件循环停顿，所以这里必须显式限时。
// 与 Session.Send 的内部行为无关——Client.SendMessage 本身也会再设一次 ctx，
// 这里是 UI 层的兜底，避免 ui 卡住看不到报错。
const sendTimeout = 5 * time.Second

// sendCmd 调 Sender.Send 把文本发到 hub，并把结果回包成 Msg 继续事件循环。
//
// 成功 → 返回 sentMsg（Update 接着再 listenCmd，把循环续上）
// 失败 → 仍返回 sentMsg（让 Update 续链），同时投递 errMsg 走错误渲染；
//
//	这里不返回 errMsg 是因为 Update 的 switch 没有「通用错误」分支，
//	错误已经进入 inbox 会被随后的 errMsg / listenCmd 自然处理。
func sendCmd(s Sender, text string, inbox chan<- tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		tuiLog.Debug("sendCmd", "len", len(text))
		if err := s.Send(ctx, text); err != nil {
			tuiLog.Error("Sender.Send failed", "err", err, "len", len(text))
			select {
			case inbox <- newErrMsg(err):
			default:
			}
		}
		return newSentMsg(text)
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
		// 尺寸就绪后才聚焦输入框（Init 阶段终端大小未知）。
		// Focused 时不再重复 focus，避免每次 resize 都重启一次光标 blink。
		var cmd tea.Cmd
		if !m.input.Focused() {
			cmd = m.FocusInput()
		}
		return m, cmd

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
		// M3.6 历史区滚屏路由：
		// textarea focus 时会拦截 Up/PgUp 等键，所以这几个按键必须由
		// 外层 Model 先抓走，转给 historyView。
		//
		// End  → 滚到底部并清零 unread（「跟到底」动作）。
		// PgUp → 整页上滚（不会到 AtBottom，所以不清 zero）。
		// PgDn → 整页下滚；若到底则 markRead。
		switch k.Code {
		case tea.KeyEnd:
			m.history.GotoBottom()
			m.MarkRead()
			return m, nil
		case tea.KeyPgUp:
			m.history.PageUp()
			return m, nil
		case tea.KeyPgDown:
			m.history.PageDown()
			if m.history.AtBottom() {
				m.MarkRead()
			}
			return m, nil
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
		// M3.9.3：5s 后自动清。schedule 一个 tea.Tick，到点投递 errExpireMsg
		// 让 Update 清掉 lastError 并触发重绘。
		m.errExpireAt = time.Now().Add(errExpireDur)
		return m, tea.Batch(listenCmd(m.inbox), m.errExpireCmd())

	case errExpireMsg:
		// 5s 到点：清错误。注意 errMsg 之间会 schedule 多个 Tick，每个到点
		// 都会触发本分支；lastError 已经是 nil 时清幂等。
		if m.lastError != nil && !time.Now().Before(m.errExpireAt) {
			m.lastError = nil
		}
		return m, listenCmd(m.inbox)

	case quitMsg:
		_ = msg.reason
		return m, tea.Cmd(tea.Quit)

	case submitMsg:
		// M3.5：有 sender 时把文本交给 Sender.Send，失败转 errMsg，成功用
		// sentMsg 续链 listenCmd；无 sender 时退回到 M3.3 行为（仅记 lastSubmitted，
		// 给单元测试观察用），保证 M3.4 之前的测试不回归。
		m.lastSubmitted = msg.text
		if m.sender == nil {
			return m, listenCmd(m.inbox)
		}
		return m, sendCmd(m.sender, msg.text, m.inbox)

	case sentMsg:
		// sendCmd 成功回执。msg.text 暂不入状态，仅续链 listenCmd。
		_ = msg.text
		return m, listenCmd(m.inbox)
	}

	return m, nil
}

// applyEvent 把 core.Event 反映到 Model 状态。
//
// M3.6 关键修正：新消息永远进 viewport 内容（refreshHistory 总会调
// SetContent），这样用户即使在最顶也能滚下去看到旧消息集合。是否跟着
// 滚到底、unread 计数如何，取决于事件**发生之前**用户是否就在底部：
//
//   - 之前在底部（wasAtBottom=true） → 跟着滚，unread 不变；
//   - 之前不在底部（wasAtBottom=false） → 保留 yoffset，unread++。
//
// 为什么用「之前」而非 SetContent 之后的 AtBottom：viewport.SetContent
// 会按新 maxYOffset clamp yoffset，新消息到来后 AtBottom 的语义会被
// 「内容增长」污染，必须锚定更新前的状态。
func (m *Model) applyEvent(e core.Event) {
	switch e.Kind {
	case core.EventState:
		if e.State != nil {
			m.connected = e.State.Connected
			if e.State.Err != nil {
				tuiLog.Error("connection state error", "connected", e.State.Connected, "err", e.State.Err)
			} else {
				tuiLog.Info("connection state changed", "connected", e.State.Connected)
			}
		}
	case core.EventMessage:
		if e.Message != nil {
			wasAtBottom := m.history.AtBottom()
			m.appendMessage(*e.Message)
			m.refreshHistory()
			if !wasAtBottom {
				m.unread++
			}
			tuiLog.Debug("message applied", "seq", e.Message.ServerSeq, "from", e.Message.SenderUserID, "len", len(e.Message.Body), "unread", m.unread)
		}
	case core.EventPresence:
		if e.Presence != nil {
			m.upsertPresence(*e.Presence)
		}
	}
}

// refreshHistory 把当前 m.messages 推给 historyView。
//
// 行为：
//   - 总是把当前消息全量写入 viewport（SetMessages）；用户滚动到任意位置
//     都能看到「已发生但未读」的消息内容。
//   - 仅在视口当前已经在底部时才主动 GotoBottom，保持「跟随」语义；
//     否则用户的滚动位置不被踢回底部。
//
// bubble 的 viewport.SetContent 不重置 yoffset，只在新 maxYOffset 变小时
// 把 yoffset 拉到新 maxYOffset，所以这里依赖 AtBottom 判断是干净的。
//
// M3.8.2 起全量刷新只发生在 WindowSizeMsg（resize 重画），单条新消息
// 由 applyEvent 走 historyView.AppendMessage 增量路径，避免每条都 split+join。
func (m *Model) refreshHistory() {
	m.history.SetMessages(m.messages)
	if m.history.AtBottom() {
		m.history.GotoBottom()
	}
}

// trySubmitInput 把当前 textInput 内容投递到 inbox，清空输入框。
//
// 三类 Noop：
//   - 空白：返回 nil cmd，不投递
//   - inbox 已满：仍写 lastSubmitted（保证观测），但丢掉消息并发 quit 提示
//   - 正常：先写入 lastSubmitted 让测试可读，再投递 submitMsg 维持 Update 协议
//
// 路由：submitMsg → Update → M3.5+ 的 client adapter 收到并 Send。
//
// M3.9.1：以 `/` 开头的输入走命令路由，不经 submitMsg。命令支持：
//   - /help    切换 helpMode，status 行展示帮助面板
//   - /clear   清空 UI 层 messages（store 不动），与 unread 一起清零
//   - /quit    直接触发 tea.Quit
//   - 其他     noop，仍写 lastSubmitted 便于调试
func (m *Model) trySubmitInput() tea.Cmd {
	text := strings.TrimRight(m.input.Value(), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	m.input.Reset()
	if strings.HasPrefix(text, "/") {
		return m.tryCommand(text)
	}
	m.lastSubmitted = text
	select {
	case m.inbox <- newSubmitMsg(text):
	default:
		m.PublishError(errInboxFull)
	}
	return listenCmd(m.inbox)
}

// tryCommand 处理 `/` 前缀的本地命令；返回的 cmd 与其他路径同协议。
//
// 帮助面板：`/help` 切换 helpMode，渲染层会拼装帮助行（见 renderStatus）；
// 不引入新的 view state，避免侵入 bubble v2 View 接口。
//
// 所有分支（含 /quit）都写 lastSubmitted 便于调试——/quit 即使触发
// tea.Quit，单元测试仍能从 LastSubmitted() 看到用户最后一次输入。
func (m *Model) tryCommand(text string) tea.Cmd {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	cmd := fields[0]
	m.lastSubmitted = text
	switch cmd {
	case "/help":
		m.helpMode = !m.helpMode
	case "/clear":
		m.messages = m.messages[:0]
		m.refreshHistory()
		m.unread = 0
	case "/quit":
		return tea.Cmd(tea.Quit)
	}
	return listenCmd(m.inbox)
}

// errInboxFull 描述 inbox 通道已满、submitMsg 被丢弃的情况。
// 定义在 model.go 顶层，Test 也能 import 引用。
var errInboxFull = errors.New("tui: inbox is full, submit dropped")

// errExpireDur 是 M3.9.3 错误展示窗口时长。
//
// 与 M3.4 sendTimeout 无关：sendTimeout 是出站 Send 的同步阻塞上限，
// errExpireDur 是 UI 层「最后一次错误」的红字显示时长。
const errExpireDur = 5 * time.Second

// errExpireCmd schedule 一个 5s 后到点的 Tick，到点投递 errExpireMsg。
//
// 失败语义：tea.Cmd 不可取消，所以 errMsg 之间会 schedule 多个 Tick；
// errExpireMsg 的 Update 分支按 expireAt 比较处理，到点 lastError 为 nil
// 时清幂等。
func (m *Model) errExpireCmd() tea.Cmd {
	return tea.Tick(errExpireDur, func(time.Time) tea.Msg {
		return newErrExpireMsg()
	})
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

// View 返回当前帧的五区拼装结果。
// M3.3 已替换占位文字 → status / hints / (history+sidebar) / input 四段纵向布局。
// M3.4 起启用 AltScreen：退出后终端不留残影，符合 TUI 应用惯例。
// M3.9.2 起 hints 行展示键位提示；视高不足时让出（见 renderLayout）。
func (m *Model) View() tea.View {
	status := m.renderStatus()
	hints := m.renderHints()
	historyView := m.history.View()
	sidebarView := m.renderSidebar()
	inputView := m.input.View()

	body := renderLayout(m.width, m.height, status, hints, historyView, sidebarView, inputView)
	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

// renderStatus 生成状态栏字符串：连接状态 + 用户/设备 + hub URL + 未读计数。
//
// M3.6 新增：当 unread > 0 时附加 `unread=N`，让用户在不切回 history 区的
// 情况下也知道有多少条新消息等着看；点 End / 滚到底部后此标记自动消失。
//
// M3.9.1：当 helpMode 为 true 时返回帮助面板字符串替代常规 status；同一
// 个 1 行 height，所以不影响布局。
//
// M3.9.3：lastError 用 lipgloss 红字渲染，到 errExpireAt 自动清掉。
func (m *Model) renderStatus() string {
	if m.helpMode {
		return "help: Enter send · Shift+Enter newline · End tail · PgUp/PgDn scroll · /help · /clear · /quit · Ctrl+C quit"
	}
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
	if m.unread > 0 {
		parts = append(parts, "unread="+itoa(m.unread))
	}
	if m.lastError != nil {
		// M3.9.3 红字渲染；过期（>5s）则清掉，View 下一帧自然不显示。
		if time.Now().Before(m.errExpireAt) {
			parts = append(parts, errStyle.Render("err="+m.lastError.Error()))
		} else {
			m.lastError = nil
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " | "
		}
		out += p
	}
	return out
}

// renderHints 生成键位提示行；M3.9.2 在 status 下方多占 1 行，
// 不依赖底层组件库，固定字符串 + lipgloss 灰字渲染。
func (m *Model) renderHints() string {
	return "[Enter] send · [Shift+Enter] newline · [End] tail · [PgUp/PgDn] scroll · [/help] commands · [Ctrl+C] quit"
}

// HelpMode 报告当前是否处于帮助面板模式（M3.9.1）。
func (m *Model) HelpMode() bool { return m.helpMode }

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

// LastSubmitted returns the most recently submitted text (set on both
// the M3.3 standalone path and the M3.5 Sender-backed path).
func (m *Model) LastSubmitted() string { return m.lastSubmitted }

// FocusInput 把光标放回输入框；外部 program 启动时调用一次。
func (m *Model) FocusInput() tea.Cmd {
	return m.input.Focus()
}

// MarkRead 把未读计数清零。End / 滚回底部 / 跟随模式下底部本身就直接
// 同步显示，所以都会调到这里。该方法幂等：unread 已经是 0 时不做事。
func (m *Model) MarkRead() {
	if m.unread != 0 {
		m.unread = 0
	}
}

// UnreadCount 返回当前累计的未读消息数；M3.6 起供 status 栏与测试用。
func (m *Model) UnreadCount() int { return m.unread }

// GotoBottom 把 history 区强制滚回底部并清零 unread。
//
// 与 Update 内的 End 键路由等价的方法形态——外部可以在收到一条
// 「用户跳到底部」事件（如跳转链接）后用同一个 API。
func (m *Model) GotoBottom() {
	m.history.GotoBottom()
	m.MarkRead()
}

// PageUp 把 history 上滚一页。M3.6 给测试与脚本式访问用。
func (m *Model) PageUp() { m.history.PageUp() }

// PageDown 把 history 下滚一页；若到底则清零 unread。
func (m *Model) PageDown() {
	m.history.PageDown()
	if m.history.AtBottom() {
		m.MarkRead()
	}
}

// ScrollUp 把 history 上滚 n 行。给自定义键盘映射留口子。
func (m *Model) ScrollUp(n int) { m.history.ScrollUp(n) }

// ScrollDown 把 history 下滚 n 行；若到底则清零 unread。
func (m *Model) ScrollDown(n int) {
	m.history.ScrollDown(n)
	if m.history.AtBottom() {
		m.MarkRead()
	}
}

// AttachSender 在 Dial 成功后接入 Sender，让 Update 看到出站事件。
//
// 必须在 tea.NewProgram 启动前调用——之后 Update 就在 bubbletea goroutine
// 上跑，submitMsg 会读 m.sender；这里没有并发争用，但调用顺序错了会导致
// 启动后前几条 submit 走 nil-sender 分支被 silently dropped。
//
// 使用示例（cmd/tui/main.go）：
//
//	m := tui.New(cfg)
//	sess, _ := tui.Dial(...)
//	m.AttachSender(sess)
//	go sess.Pump(ctx, m.Publish)
//	p := tea.NewProgram(m)
func (m *Model) AttachSender(s Sender) { m.sender = s }

// Sender returns the currently attached sender (nil if no live link).
//
// 测试与诊断用；运行时一般不需要。
func (m *Model) Sender() Sender { return m.sender }

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
