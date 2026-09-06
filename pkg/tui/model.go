package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

	// Translator 是 UI 文案本地化接口（M3.10）；nil 时 Model 用 nopTranslator
	// fallback，UI 显示原始 key 串。cmd/tui 启动期会注入 i18n.Bundle.ForLocale。
	Translator Translator
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
	translator           Translator // M3.10：UI 文案本地化接口

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

	// M7.1：上翻分页状态。loadingOlder 防止重复在途请求；olderHasMore
	// 由 hub 响应的 HasMore 维护，false 后不再触发（到头）。
	loadingOlder bool
	olderHasMore bool

	// M7.3：「正在输入」。typings 记录对端设备最后一次 typing 时间，
	// 到 typingTTL 过期；lastTypingAt 是本端上发节流位点。
	typings      map[string]typingState
	lastTypingAt time.Time

	// M8.1：「已读回执」。reads 记录其它设备已读到的 ServerSeq（Hub 盖戳
	// 广播 + 握手快照补发），按 DeviceID 单调合并。与 typings 不同：已读
	// 是持久语义，设备离线不清除——重连后 Hub 会重发快照，单调合并保证
	// 不回退。
	reads map[string]uint64
}

// typingState 是一条对端「正在输入」记录（M7.3）。
type typingState struct {
	user string
	at   time.Time
}

// New 构造一个未连接、待 Init 的 Model。
//
// 默认 cfg.Translator 用 defaultENTranslator（pkg/tui 内置 14 个 key 的
// 英文文案），单元测试无需每处都注入；cmd/tui 启动期会被
// i18n.Bundle.ForLocale 覆盖。
func New(cfg Config) *Model {
	if cfg.MaxHist <= 0 {
		cfg.MaxHist = 5000
	}
	if cfg.Translator == nil {
		cfg.Translator = defaultENTranslator{}
	}
	m := &Model{
		user:         cfg.User,
		device:       cfg.Device,
		hubURL:       cfg.HubURL,
		maxHist:      cfg.MaxHist,
		inbox:        make(chan tea.Msg, 64),
		sender:       cfg.Sender,
		translator:   cfg.Translator,
		input:        newTextInput(cfg.Translator),
		history:      newHistoryView(cfg.Translator),
		olderHasMore: true,
		typings:      make(map[string]typingState),
		reads:        make(map[string]uint64),
	}
	// M8.1：historyView 的已读标记判定由 Model 注入（读 m.reads 快照，
	// 与其它状态一样只在 Update goroutine 内访问，无需加锁）。
	m.history.SetReadChecker(m.readByOthers)
	return m
}

// t 是 Model 内部的文案查表 helper：把 key 转给 Translator，
// nil-safe 由 New 时 nopTranslator 兜底。
//
// 所有 UI 文案都走这条路径，让 fake translator 测试能集中断言 key 调用。
func (m *Model) t(key string) string {
	return m.translator.T(key)
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

// fetchOlderTimeout 是上翻分页请求的硬性上限（M7.1）。
const fetchOlderTimeout = 10 * time.Second

// fetchOlderLimit 是单次上翻拉取的条数，与 web 端「加载更多」同量级。
const fetchOlderLimit = 50

// typingThrottle 是本端「正在输入」上发节流间隔（M7.3）：持续输入时
// 最多每 typingThrottle 打一帧，避免每个按键都发包。
const typingThrottle = 3 * time.Second

// typingTTL 是对端 typing 状态的展示有效期（M7.3）：对端持续输入会
// 不断刷新，停止输入（或断连）后最迟 typingTTL 内指示消失。取节流
// 间隔的 2 倍，容忍丢一帧。
const typingTTL = 2 * typingThrottle

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

// fetchOlderCmd 调 HistoryFetcher 拉取 before 之前的一页历史（M7.1）。
//
// 与 sendCmd 不同，结果直接作为 tea.Msg 返回给 Update（不经 inbox）：
// inbox 的 listenCmd 循环独立续期，分页响应是一次性的，没必要占 inbox 槽位。
func fetchOlderCmd(f HistoryFetcher, before uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchOlderTimeout)
		defer cancel()
		msgs, hasMore, err := f.FetchHistory(ctx, before, fetchOlderLimit)
		if err != nil {
			tuiLog.Warn("FetchHistory failed", "before", before, "err", err)
		}
		return olderMessagesMsg{msgs: msgs, hasMore: hasMore, err: err}
	}
}

// typingCmd 调 Typer.SendTyping 上发「正在输入」（M7.3）。
// fire-and-forget：失败只 Debug 日志，返回 typingSentMsg 占位续事件循环。
func typingCmd(t Typer) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := t.SendTyping(ctx); err != nil {
			tuiLog.Debug("SendTyping failed", "err", err)
		}
		return typingSentMsg{}
	}
}

// maybeNotifyTyping 在用户持续输入时节流上发 typing 帧（M7.3）。
//
// 条件：输入框非空、距上次上发 ≥ typingThrottle、sender 实现 Typer。
// 导航键（PgUp/End 等）在更外层已被截走，到这里的都是输入类按键。
func (m *Model) maybeNotifyTyping() tea.Cmd {
	if m.input.Value() == "" {
		return nil
	}
	if !m.lastTypingAt.IsZero() && time.Since(m.lastTypingAt) < typingThrottle {
		return nil
	}
	t, ok := m.sender.(Typer)
	if !ok {
		return nil
	}
	m.lastTypingAt = time.Now()
	return typingCmd(t)
}

// maybeFetchOlder 在视口已到顶部且条件满足时发起上翻分页；否则返回 nil。
//
// 条件：无在途请求、hub 仍报告 HasMore、本地已有消息且最早一条带 seq、
// sender 实现 HistoryFetcher。
func (m *Model) maybeFetchOlder() tea.Cmd {
	if m.loadingOlder || !m.olderHasMore || len(m.messages) == 0 {
		return nil
	}
	// 只在视口已经贴顶时才拉：用户还在中段翻页时提前请求纯属浪费。
	if !m.history.AtTop() {
		return nil
	}
	f, ok := m.sender.(HistoryFetcher)
	if !ok {
		return nil
	}
	before := m.messages[0].ServerSeq
	if before == 0 {
		return nil
	}
	m.loadingOlder = true
	return fetchOlderCmd(f, before)
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
		// 小键盘 Enter 也是同样的语义：bubbletea 把它解为不同的 KeyCode
		// (tea.KeyKpEnter)，老代码只判 KeyEnter 所以漏过；这里补上。
		if k.Code == tea.KeyEnter || k.Code == tea.KeyKpEnter {
			if k.Mod&tea.ModShift == 0 {
				return m, m.trySubmitInput()
			}
			// Shift+Enter / Shift+小键盘 Enter：继续往下到 textInput 触发换行。
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
			return m, m.readCmd(m.latestSeq())
		case tea.KeyPgUp:
			m.history.PageUp()
			// M7.1：翻到视口顶部时顺手拉更早的历史（有在途请求/已到头则 no-op）。
			return m, m.maybeFetchOlder()
		case tea.KeyPgDown:
			m.history.PageDown()
			if m.history.AtBottom() {
				m.MarkRead()
				return m, m.readCmd(m.latestSeq())
			}
			return m, nil
		}
		// 其他键自动 focus textInput 后透传过去——首次按键即激活输入框。
		var cmds []tea.Cmd
		if !m.input.Focused() {
			cmds = append(cmds, m.input.Focus())
		}
		cmds = append(cmds, m.input.Update(msg))
		// M7.3：输入时节流通知「正在输入」（输入框为空 / 节流窗口内 no-op）。
		cmds = append(cmds, m.maybeNotifyTyping())
		return m, tea.Batch(cmds...)

	case eventMsg:
		m.applyEvent(msg.event)
		cmds := []tea.Cmd{listenCmd(m.inbox)}
		// M7.3：typing 事件安排一个过期 Tick（事件已 upsert，到点清条目）。
		if msg.event.Kind == core.EventTyping {
			cmds = append(cmds, m.typingExpireCmd())
		}
		// M8.1：他人新消息到达且用户正贴底跟随 → 视为已读，上发回执
		// （hub 盖戳身份广播给其它设备；自己消息回环不重复上发）。
		if msg.event.Kind == core.EventMessage && msg.event.Message != nil &&
			msg.event.Message.SenderUserID != m.user && m.history.AtBottom() {
			cmds = append(cmds, m.readCmd(msg.event.Message.ServerSeq))
		}
		return m, tea.Batch(cmds...)

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

	case olderMessagesMsg:
		// M7.1：上翻分页响应。本 Msg 来自 fetchOlderCmd 而非 inbox，
		// listenCmd 循环仍挂着，这里不续链。
		m.loadingOlder = false
		if msg.err != nil {
			tuiLog.Warn("fetch older history failed", "err", msg.err)
			m.lastError = msg.err
			m.errExpireAt = time.Now().Add(errExpireDur)
			return m, nil
		}
		m.olderHasMore = msg.hasMore
		if len(msg.msgs) > 0 {
			m.prependMessages(msg.msgs)
		}
		return m, nil

	case typingSentMsg:
		// M7.3：本端 typing 上发回执，无状态要更新。
		return m, nil

	case typingExpireMsg:
		// M7.3：清掉过期的对端 typing 条目；还有未过期的就再 schedule
		// 一个 Tick（每个事件一个 Tick 的模型下，最新事件的 Tick 到点时
		// 其余条目必然也已过期或同样被覆盖）。
		if m.pruneTyping() > 0 {
			return m, m.typingExpireCmd()
		}
		return m, nil
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
			// 对方消息到达即说明输入结束，立刻撤掉该设备的 typing 指示。
			delete(m.typings, e.Message.SenderDeviceID)
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
	case core.EventTyping:
		if e.Typing != nil {
			m.upsertTyping(*e.Typing)
		}
	case core.EventRead:
		// M8.1：他人已读回执推进 → 重渲历史，让「✓已读」标记出现/前进。
		// upsertRead 单调合并，旧帧/乱序帧返回 false 不触发重渲。
		if e.Read != nil && m.upsertRead(*e.Read) {
			m.refreshHistory()
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
	// M7.3：消息发出后下一段输入应能立刻触发 typing，重置节流位点。
	m.lastTypingAt = time.Time{}
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

// prependMessages 把上翻拉回的更早消息合并到 m.messages 头部（M7.1）。
//
// hub 返回升序；按消息 ID 去重（catch-up 与分页窗口可能重叠），新内容
// 交 historyView.PrependMessages 渲染并做 YOffset 视觉锚点补偿。
func (m *Model) prependMessages(older []protocol.StoredMessage) {
	have := make(map[string]struct{}, len(m.messages))
	for _, msg := range m.messages {
		have[msg.ID] = struct{}{}
	}
	add := make([]protocol.StoredMessage, 0, len(older))
	for _, msg := range older {
		if _, dup := have[msg.ID]; dup {
			continue
		}
		add = append(add, msg)
	}
	if len(add) == 0 {
		return
	}
	merged := make([]protocol.StoredMessage, 0, len(add)+len(m.messages))
	merged = append(merged, add...)
	merged = append(merged, m.messages...)
	m.messages = merged
	m.history.PrependMessages(add)
}

// upsertPresence 按 DeviceID upsert 在线设备。
func (m *Model) upsertPresence(p protocol.Presence) {
	// 设备离线 → 其「正在输入」指示一并撤掉（无论 peers 列表里是否已有它）。
	if !p.Online {
		delete(m.typings, p.DeviceID)
	}
	for i := range m.peers {
		if m.peers[i].DeviceID == p.DeviceID {
			m.peers[i] = p
			return
		}
	}
	m.peers = append(m.peers, p)
}

// upsertTyping 记录/刷新一条对端「正在输入」（M7.3）。同一设备连续
// typing 只刷新时间；过期 Tick 由 Update 的 eventMsg 分支统一 schedule，
// typingTTL 后无新事件则指示消失。
func (m *Model) upsertTyping(ty protocol.Typing) {
	if ty.DeviceID == "" {
		return
	}
	m.typings[ty.DeviceID] = typingState{user: ty.UserID, at: time.Now()}
}

// pruneTyping 清掉超过 typingTTL 的 typing 条目，返回剩余条数。
func (m *Model) pruneTyping() int {
	now := time.Now()
	for dev, st := range m.typings {
		if now.Sub(st.at) >= typingTTL {
			delete(m.typings, dev)
		}
	}
	return len(m.typings)
}

// typingExpireCmd schedule 一个 typingTTL 后的过期检查 Tick。
func (m *Model) typingExpireCmd() tea.Cmd {
	return tea.Tick(typingTTL, func(time.Time) tea.Msg {
		return typingExpireMsg{}
	})
}

// typingNames 返回当前正在输入的用户名列表（去重、字典序，渲染稳定）。
// 顺带清掉已过期条目，让 View 读到的就是最新状态。
func (m *Model) typingNames() []string {
	m.pruneTyping()
	seen := make(map[string]struct{}, len(m.typings))
	names := make([]string, 0, len(m.typings))
	for _, st := range m.typings {
		name := st.user
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// upsertRead 单调合并一条他人已读回执（M8.1）：同设备游标只进不退。
// 自己设备的回执（Hub 广播已排除发送者）与空设备号/零 seq 直接忽略。
// 返回 true 表示快照有变化，调用方据此重渲历史。
func (m *Model) upsertRead(rc protocol.ReadCursor) bool {
	if rc.DeviceID == "" || rc.DeviceID == m.device || rc.ServerSeq == 0 {
		return false
	}
	if cur, ok := m.reads[rc.DeviceID]; ok && cur >= rc.ServerSeq {
		return false
	}
	m.reads[rc.DeviceID] = rc.ServerSeq
	return true
}

// readByOthers 报告「我发的某条消息是否已被其它设备读到」（M8.1）：
// 任一其它设备的已读游标 >= 该消息 seq 即视为已读。非本人消息、seq
// 无效或没有游标越过时返回 false。historyView 的已读标记判定入口。
func (m *Model) readByOthers(sender string, seq uint64) bool {
	if sender == "" || sender != m.user || seq == 0 {
		return false
	}
	for _, s := range m.reads {
		if s >= seq {
			return true
		}
	}
	return false
}

// readCmd 上发「已读到 seq」回执（M8.1）。sender 未实现 Reader 或 seq
// 无效时返回 nil；fire-and-forget：失败只 Debug 日志，下次贴底会再触发。
func (m *Model) readCmd(seq uint64) tea.Cmd {
	r, ok := m.sender.(Reader)
	if !ok || seq == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := r.SendRead(ctx, seq); err != nil {
			tuiLog.Debug("SendRead failed", "err", err)
		}
		return readSentMsg{}
	}
}

// latestSeq 返回历史里最新一条消息的 ServerSeq（无消息时 0）。
func (m *Model) latestSeq() uint64 {
	if n := len(m.messages); n > 0 {
		return m.messages[n-1].ServerSeq
	}
	return 0
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
		return m.t("tui.help.row")
	}
	conn := m.t("tui.status.offline")
	if m.connected {
		conn = m.t("tui.status.online")
	}
	parts := []string{
		conn,
		m.t("tui.status.label.user") + "=" + m.user,
		m.t("tui.status.label.device") + "=" + m.device,
		m.t("tui.status.label.hub") + "=" + m.hubURL,
	}
	if m.unread > 0 {
		parts = append(parts, m.t("tui.status.label.unread")+"="+itoa(m.unread))
	}
	if m.lastError != nil {
		// M3.9.3 红字渲染；过期（>5s）则清掉，View 下一帧自然不显示。
		if time.Now().Before(m.errExpireAt) {
			parts = append(parts, errStyle.Render(m.t("tui.status.label.err")+"="+m.lastError.Error()))
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
//
// M3.10：文案走 Translator；fallback en 行宽约 80 字符，窄终端会被
// lipgloss 截断（详见 layout_test.go 的 widthConsistent）。
//
// M7.3：有人正在输入时，hints 行让位给 typing 指示——键位提示是
// 学一次的东西，typing 是此刻该看的东西；停止输入 typingTTL 后自动恢复。
func (m *Model) renderHints() string {
	if names := m.typingNames(); len(names) > 0 {
		key := "tui.typing.many"
		if len(names) == 1 {
			key = "tui.typing.one"
		}
		return fmt.Sprintf(m.t(key), strings.Join(names, ", "))
	}
	return m.t("tui.hints.row")
}

// HelpMode 报告当前是否处于帮助面板模式（M3.9.1）。
func (m *Model) HelpMode() bool { return m.helpMode }

// renderSidebar 生成右侧在线设备列表文本。
//
// M3.10：empty 文案走 Translator。zh-CN "peers：（暂无）" 与 en
// "peers: (none yet)" 行宽相近，layout 不受影响。
func (m *Model) renderSidebar() string {
	if len(m.peers) == 0 {
		return m.t("tui.sidebar.empty")
	}
	online := 0
	for _, p := range m.peers {
		if p.Online {
			online++
		}
	}
	out := m.t("tui.sidebar.prefix") + " " + itoa(online) + "/" + itoa(len(m.peers)) + "\n"
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
