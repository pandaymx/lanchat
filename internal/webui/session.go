package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/e2e"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// DialOptions 是 DialClient 的入参。Transport 必填，其余有兜底。
type DialOptions struct {
	// Transport 决定底层连接如何建立：生产用 pkg/transport/ws，
	// 测试用 pkg/transport/fake。这就是 ADR-002 的可替换点。
	Transport core.Transport
	// HubURL 形如 ws://127.0.0.1:9000（ws 实现会把 path 规范化成 /ws）。
	HubURL string
	// User / Device 组成 ADR-008 的二层身份；Device 同时是 ReadCursor 主键。
	User   string
	Device string
	// HistoryLimit 是首屏历史拉取条数，<=0 用 client 默认值（50）。
	HistoryLimit int
	// E2EDataDir 是本端 E2E 身份存放目录；非空时按设备创建独立身份
	// 文件 e2e_<device>.bin（每个浏览器 Session/设备一份），启用端到端
	// 加密。加载失败仅警告、回退明文，不阻断连接。
	E2EDataDir string
}

// DialClient 建立到 hub 的连接并完成握手（Hello + 历史补发）。
//
// 装配模式仿 pkg/tui.Dial：transport.Dial → memory store → event bus →
// client.New → Connect(RequestHistory)。web 端不内嵌 hub 逻辑，只作为一个
// 普通客户端接 hub（提案 §3.2 thin proxy）。
//
// 成功返回的 client 与 store 由调用方释放，顺序必须是先 cli.Close() 再
// store.Close()：先停 readPump 再关 store，避免往已关闭的 store 里写。
// Dial 成功但 Connect 失败时，conn 与 store 已在函数内回收，无需调用方处理。
func DialClient(ctx context.Context, opts DialOptions) (*client.Client, core.Store, error) {
	if opts.Transport == nil {
		return nil, nil, errors.New("webui: DialOptions.Transport is required")
	}

	hello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        opts.Device,
		UserID:          opts.User,
	}

	conn, err := opts.Transport.Dial(ctx, opts.HubURL, hello)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", opts.HubURL, err)
	}

	store := memory.New()
	bus := event.New()
	cli := client.New(hello, conn, store, bus)

	// M9：文件传输的数据面挂在 hub 的 HTTP 端口（与 WS 同端口），
	// 从 ws URL 推导 http 基址注入 client；不额外要求第二个地址。
	cli.SetFileBase(client.HTTPBaseFromWS(opts.HubURL))

	// E2E：每个设备（web cookie 对应一个 device）独立身份文件，
	// 加载/创建后向 hub keyring 注册公钥（失败只警告，渐进明文）。
	if opts.E2EDataDir != "" {
		idPath := filepath.Join(opts.E2EDataDir, "e2e_"+opts.Device+".bin")
		e2eID, err := e2e.LoadOrCreateIdentity(idPath)
		if err != nil {
			logging.New("webui").Warn("e2e identity load failed, plaintext", "path", idPath, "err", err)
		} else if err := cli.SetE2E(e2eID); err != nil {
			logging.New("webui").Warn("e2e init failed, plaintext", "err", err)
		}
	}

	if err := cli.Connect(ctx, client.ConnectOptions{
		RequestHistory: true,
		HistoryLimit:   opts.HistoryLimit,
		// 首页渲染直接读本地 Store；同步等连接时历史落库，
		// 避免竞态窗口导致首屏永远空历史（见 client.go ConnectOptions.WaitHistory）。
		WaitHistory: true,
	}); err != nil {
		_ = conn.Close()
		_ = store.Close()
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	return cli, store, nil
}

// defaultDeviceName 生成 web 端设备标识：web-<8 位随机十六进制>。
//
// 一枚 cookie 一份 Session，device 在 Session 创建时生成并随 cookie 稳定，
// 符合 ADR-008（同 user 多设备各自独立 ReadCursor）；浏览器关 tab 重开若
// cookie 仍在则复用同一 device。
func defaultDeviceName() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 读失败意味着系统熵源不可用，属于严重环境问题；
		// 这里不 panic 而是退回固定串，避免 web server 起不来。
		return "web-unknown"
	}
	return fmt.Sprintf("web-%x", b)
}

// Dialer 是 Manager 建立一条 hub 连接的函数签名。
//
// 生产用 DefaultDialer（包 DialClient）；测试注入返回 stubClient 的假 dialer，
// 不必起 hub。这是 Manager 层的 ADR-002 开关点。
type Dialer func(ctx context.Context, opts DialOptions) (Client, core.Store, error)

// HubHTTPBase 返回 hub 的 HTTP 基址（M9 文件端点挂在与 WS 同端口）。
// Handler 的文件上传/下载代理端点据此转发请求，不要求用户配第二个地址。
func (m *Manager) HubHTTPBase() string {
	return client.HTTPBaseFromWS(m.cfg.HubURL)
}

// DefaultDialer 把 DialClient 的具体返回类型适配成 Dialer 窄签名。
func DefaultDialer(ctx context.Context, opts DialOptions) (Client, core.Store, error) {
	cli, store, err := DialClient(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	return cli, store, nil
}

// ManagerConfig 是 Manager 的构造入参。零值字段在 NewManager 里落默认。
type ManagerConfig struct {
	// HubURL / User / ConvID / Transport 透传给每次拨号。
	HubURL    string
	User      string
	ConvID    string
	Transport core.Transport
	// HistoryLimit 是首屏历史条数，<=0 用 historyLimit（50）。
	HistoryLimit int
	// E2EDataDir 透传给每次拨号（非空启用 E2E，按设备建身份文件）。
	E2EDataDir string
	// DialTimeout 是单次拨号（含握手）上限，<=0 用 defaultDialTimeout。
	DialTimeout time.Duration
	// SessionTTL 是 Session 无活动多久后被 janitor 回收，<=0 用 defaultSessionTTL。
	SessionTTL time.Duration
	// SweepInterval 是 janitor 扫描间隔，<=0 用 defaultSweepInterval；
	// 测试里故意调大以关掉后台扫描，改用手动 SweepExpired。
	SweepInterval time.Duration
	// Translator 是 UI chrome 文案翻译器（M4.7），透传给每个 Session；
	// SSE state 帧渲染 ConnStatus 时用。nil 时模板兜底返回 key 字面值。
	Translator templates.Translator
	// OnMessage 是每条实时新消息（core.EventMessage，不含历史回放与
	// 自己 catchUp 补发）的可选回调；桌面端用它弹系统通知，web 端保持
	// nil。回调在事件泵 goroutine 上同步调用，必须快速返回（内部投递
	// channel，不要做 IO/加锁重活）。
	OnMessage func(*protocol.StoredMessage)
}

const (
	defaultDialTimeout   = 5 * time.Second
	defaultSessionTTL    = 30 * time.Minute
	defaultSweepInterval = 5 * time.Minute
)

// Manager 按 cookie 管理 Session：惰性拨号创建、复用、TTL 回收、全量关闭。
//
// 生命周期：NewManager 起一个 janitor goroutine；CloseAll 停 janitor 并
// 释放全部 Session（进程退出时调）。
type Manager struct {
	cfg    ManagerConfig
	dial   Dialer
	logger *logging.ComponentLogger

	mu       sync.Mutex
	sessions map[string]*Session

	closeCh   chan struct{}
	closeOnce sync.Once
}

// NewManager 构造 Manager 并启动 janitor。dial 为 nil 时用 DefaultDialer。
func NewManager(cfg ManagerConfig, dial Dialer) *Manager {
	if dial == nil {
		dial = DefaultDialer
	}
	if cfg.ConvID == "" {
		cfg.ConvID = DefaultConversationID
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = defaultSweepInterval
	}
	m := &Manager{
		cfg:      cfg,
		dial:     dial,
		logger:   logging.New("web"),
		sessions: make(map[string]*Session),
		closeCh:  make(chan struct{}),
	}
	go m.janitor()
	return m
}

// GetOrCreate 返回 cookie 对应的 Session；不存在（或已死）则惰性拨号新建。
//
// 拨号是慢操作（网络 + 握手），故意放在锁外：并发请求带不同 cookie 时
// 互不阻塞；同一 cookie 并发首请求可能各自拨号，create 里双检后关掉多余的。
// 拨号失败返回错误，调用方回 502/500，浏览器重试即可。
func (m *Manager) GetOrCreate(ctx context.Context, cookie string) (*Session, error) {
	m.mu.Lock()
	sess, ok := m.sessions[cookie]
	m.mu.Unlock()
	if ok {
		if sess.alive() {
			sess.touch()
			return sess, nil
		}
		// 死 session（hub 断开过）：摘除释放，落到下面重建。
		m.mu.Lock()
		if cur, ok2 := m.sessions[cookie]; ok2 && cur == sess {
			delete(m.sessions, cookie)
		}
		m.mu.Unlock()
		sess.shutdown()
	}
	return m.create(ctx, cookie)
}

// create 拨号并登记新 Session。锁外拨号，锁内双检。
//
// ctx 语义（两个 transport 的约定不同，这里统一）：
//   - ws transport：Dial ctx 只用于拨号阶段，连接建立后独立；
//   - fake transport：Dial ctx 即连接生命周期（Router 读循环 ctx.Done 即注销）。
//
// 所以 Session 持有自己的生命周期 ctx（sessCtx），拨号用它；DialTimeout
// 通过 goroutine + timer 包裹实现，超时才 cancel sessCtx（此时连接尚未建成，
// cancel 安全；ws 下还能中断挂死的拨号）。
func (m *Manager) create(ctx context.Context, cookie string) (*Session, error) {
	device := defaultDeviceName()
	// WithoutCancel：ctx 链路保留（trace 等 value），但 session 脱离
	// 派生它的 HTTP 请求生命周期——请求结束不能杀连接（fake transport 的
	// Router 读循环绑定 Dial ctx，见下注释）。
	sessCtx, sessCancel := context.WithCancel(context.WithoutCancel(ctx))

	type dialResult struct {
		cli   Client
		store core.Store
		err   error
	}
	resCh := make(chan dialResult, 1)
	go func() {
		cli, store, err := m.dial(sessCtx, DialOptions{
			Transport:    m.cfg.Transport,
			HubURL:       m.cfg.HubURL,
			User:         m.cfg.User,
			Device:       device,
			HistoryLimit: m.cfg.HistoryLimit,
			E2EDataDir:   m.cfg.E2EDataDir,
		})
		resCh <- dialResult{cli: cli, store: store, err: err}
	}()

	var res dialResult
	timer := time.NewTimer(m.cfg.DialTimeout)
	defer timer.Stop()
	select {
	case res = <-resCh:
	case <-timer.C:
		sessCancel()
		m.logger.Error("dial hub timeout", "cookie", cookie, "timeout", m.cfg.DialTimeout)
		return nil, fmt.Errorf("dial hub: timeout after %s", m.cfg.DialTimeout)
	}
	if res.err != nil {
		sessCancel()
		m.logger.Error("dial hub failed", "cookie", cookie, "err", res.err)
		return nil, fmt.Errorf("dial hub: %w", res.err)
	}

	sess := &Session{
		id:        cookie,
		user:      m.cfg.User,
		device:    device,
		convID:    m.cfg.ConvID,
		cli:       res.cli,
		store:     res.store,
		lastSeen:  time.Now(),
		writers:   make(map[*sseWriter]struct{}),
		convs:     make(map[string]*convMeta),
		ctx:       sessCtx,
		cancel:    sessCancel,
		onMessage: m.cfg.OnMessage,
		tr:        m.cfg.Translator,
		logger:    logging.New("web"),
	}
	sess.startPump()

	m.mu.Lock()
	// 双检：拨号期间可能已有另一个请求建好了同 cookie 的活 session。
	if existing, ok := m.sessions[cookie]; ok && existing.alive() {
		m.mu.Unlock()
		sess.shutdown()
		existing.touch()
		m.logger.Warn("duplicate session dial discarded", "cookie", cookie)
		return existing, nil
	}
	m.sessions[cookie] = sess
	m.mu.Unlock()

	m.logger.Info("session created", "cookie", cookie, "device", device, "hub", m.cfg.HubURL)
	return sess, nil
}

// Release 主动关闭并摘除指定 cookie 的 Session。幂等：不存在是空操作。
func (m *Manager) Release(cookie string) {
	m.mu.Lock()
	sess, ok := m.sessions[cookie]
	if ok {
		delete(m.sessions, cookie)
	}
	m.mu.Unlock()
	if ok {
		sess.shutdown()
		m.logger.Info("session released", "cookie", cookie)
	}
}

// SweepExpired 回收两类死 Session：
//   - hub 连接已断（cli.Done）；
//   - 超过 SessionTTL 没有任何 HTTP 活动（lastSeen）。
//
// 导出是为了测试可手动触发；生产由 janitor 周期调用。
func (m *Manager) SweepExpired() {
	now := time.Now()
	var dead []*Session
	m.mu.Lock()
	for id, s := range m.sessions {
		if !s.alive() || now.Sub(s.lastSeenAt()) > m.cfg.SessionTTL {
			dead = append(dead, s)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, s := range dead {
		s.shutdown()
		m.logger.Info("session swept", "cookie", s.id, "age", now.Sub(s.lastSeenAt()).Round(time.Second))
	}
}

// CloseAll 停 janitor 并释放全部 Session。进程退出时调，幂等。
func (m *Manager) CloseAll() {
	m.closeOnce.Do(func() { close(m.closeCh) })
	m.mu.Lock()
	all := m.sessions
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
	for _, s := range all {
		s.shutdown()
	}
}

// janitor 周期 SweepExpired，直到 CloseAll。
func (m *Manager) janitor() {
	ticker := time.NewTicker(m.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.closeCh:
			return
		case <-ticker.C:
			m.SweepExpired()
		}
	}
}

// Session 是 web server 端「一枚 cookie 一份」的客户端会话。
//
// 内部装配与 cmd/tui 的 tui.Session 同构（pkg/client.Client + memory store +
// event bus），不同点：web Session 不绑定渲染 goroutine——事件由 pump
// goroutine 统一订阅、渲染一次、fanout 给所有 SSE 连接（多 tab 共享靠它）。
type Session struct {
	id     string
	user   string
	device string
	convID string
	cli    Client
	store  core.Store

	mu           sync.Mutex
	lastSeen     time.Time
	dead         bool
	writers      map[*sseWriter]struct{}
	shutdownOnce sync.Once

	// convMetaMu 保护 convs 数据层（事件泵 goroutine 与 HTTP handler 并发）。
	convMetaMu sync.Mutex
	convs      map[string]*convMeta

	// ctx / cancel 是 Session 级生命周期 ctx：拨号与 pump 都用它
	// （fake transport 的 Router 读循环绑定 Dial ctx）。shutdown 时 cancel。
	ctx    context.Context
	cancel context.CancelFunc

	// onMessage 见 ManagerConfig.OnMessage；nil 时 startPump 跳过。
	onMessage func(*protocol.StoredMessage)

	// tr 是 UI chrome 文案翻译器（M4.7），SSE state 帧渲染 ConnStatus 用；
	// 装配遗漏时为 nil，templates.T 兜底返回 key 字面值。
	tr templates.Translator

	logger *logging.ComponentLogger
}

// convMeta 是会话列表数据层（v3.0）的一条记录：最后消息预览/时间/未读数。
// 由事件泵在运行期累积；首屏缺省时 handler 用历史回填。
type convMeta struct {
	lastBody string
	lastAtMs int64
	unread   int
}

// touch 刷新最后活动时间（每个 HTTP 请求 / SSE 连接建立时调）。
func (s *Session) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

func (s *Session) lastSeenAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen
}

// alive 报告 Session 是否可用；顺手把 cli.Done 的 session 标记为 dead。
func (s *Session) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return false
	}
	select {
	case <-s.cli.Done():
		s.dead = true
		return false
	default:
		return true
	}
}

// markDead 由 pump 在 hub 连接断开时调用。
func (s *Session) markDead() {
	s.mu.Lock()
	s.dead = true
	s.mu.Unlock()
}

// touchConv 记录一条消息对会话列表数据层的贡献：更新最后消息预览与
// 时间；消息不属于当前会话时未读 +1（当前会话即时可见，不计角标）。
// 预览是原始 body（文件/语音等空 body 消息由上层给占位文案）。
func (s *Session) touchConv(convID, body string, atMs int64) {
	s.convMetaMu.Lock()
	m := s.convs[convID]
	if m == nil {
		m = &convMeta{}
		s.convs[convID] = m
	}
	m.lastBody = body
	m.lastAtMs = atMs
	if convID != s.activeConv() {
		m.unread++
	}
	s.convMetaMu.Unlock()
}

// setConv 切换当前会话（handleHome 每次进入会话时调）：更新会话身份
// 供事件泵判定未读归属，并清零新会话的未读（看到即已读）。
func (s *Session) setConv(convID string) {
	s.mu.Lock()
	s.convID = convID
	s.mu.Unlock()
	s.clearUnread(convID)
}

// activeConv 返回当前会话（事件泵判定未读归属用）。
func (s *Session) activeConv() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.convID
}

// clearUnread 清零某会话未读数（进入会话时调用：看到即已读）。
func (s *Session) clearUnread(convID string) {
	s.convMetaMu.Lock()
	if m := s.convs[convID]; m != nil {
		m.unread = 0
	}
	s.convMetaMu.Unlock()
}

// backfillConv 首屏历史回填：该会话还没有数据层记录时用最近一条消息
// 填充预览/时间（未读从 0 计——首次进入会话即已读）。返回是否回填。
func (s *Session) backfillConv(convID, body string, atMs int64) bool {
	s.convMetaMu.Lock()
	defer s.convMetaMu.Unlock()
	if _, ok := s.convs[convID]; ok {
		return false
	}
	s.convs[convID] = &convMeta{lastBody: body, lastAtMs: atMs}
	return true
}

// convMetaOf 是 ConvMetaFn 的实现：返回某会话的数据层三要素。
func (s *Session) convMetaOf(convID string) (string, int64, int) {
	s.convMetaMu.Lock()
	defer s.convMetaMu.Unlock()
	if m := s.convs[convID]; m != nil {
		return m.lastBody, m.lastAtMs, m.unread
	}
	return "", 0, 0
}

// shutdown 释放底层连接。顺序铁律（同 DialClient 注释）：先 cli.Close()
// 停 readPump，再 store.Close()。幂等。
func (s *Session) shutdown() {
	s.shutdownOnce.Do(func() {
		s.markDead()
		s.closeWriters()
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.cli.Close()
		_ = s.store.Close()
	})
}
