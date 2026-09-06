package webui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
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

	if err := cli.Connect(ctx, client.ConnectOptions{
		RequestHistory: true,
		HistoryLimit:   opts.HistoryLimit,
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
	// DialTimeout 是单次拨号（含握手）上限，<=0 用 defaultDialTimeout。
	DialTimeout time.Duration
	// SessionTTL 是 Session 无活动多久后被 janitor 回收，<=0 用 defaultSessionTTL。
	SessionTTL time.Duration
	// SweepInterval 是 janitor 扫描间隔，<=0 用 defaultSweepInterval；
	// 测试里故意调大以关掉后台扫描，改用手动 SweepExpired。
	SweepInterval time.Duration
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
func (m *Manager) create(ctx context.Context, cookie string) (*Session, error) {
	device := defaultDeviceName()
	dialCtx, cancel := context.WithTimeout(ctx, m.cfg.DialTimeout)
	cli, store, err := m.dial(dialCtx, DialOptions{
		Transport:    m.cfg.Transport,
		HubURL:       m.cfg.HubURL,
		User:         m.cfg.User,
		Device:       device,
		HistoryLimit: m.cfg.HistoryLimit,
	})
	cancel()
	if err != nil {
		m.logger.Error("dial hub failed", "cookie", cookie, "err", err)
		return nil, fmt.Errorf("dial hub: %w", err)
	}

	sess := &Session{
		id:       cookie,
		user:     m.cfg.User,
		device:   device,
		convID:   m.cfg.ConvID,
		cli:      cli,
		store:    store,
		lastSeen: time.Now(),
		logger:   logging.New("web"),
	}
	// startPump 在 fanout 提交里接上（Session 事件泵）。

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
	shutdownOnce sync.Once

	logger *logging.ComponentLogger
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

// shutdown 释放底层连接。顺序铁律（同 DialClient 注释）：先 cli.Close()
// 停 readPump，再 store.Close()。幂等。
func (s *Session) shutdown() {
	s.shutdownOnce.Do(func() {
		s.markDead()
		_ = s.cli.Close()
		_ = s.store.Close()
	})
}
