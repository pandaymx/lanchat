// Package client 是 core.Store + core.Conn + core.EventBus 的高层组合，
// 把"连上 Hub / 发消息 / 收订阅"包装成业务侧一句 API。
//
// 这是 M1 的 reference 实现。M2 起会演进出真 Hub 客户端，但它永远不依赖具体
// Transport/Store/EventBus 的实现 —— 这就是 ADR-002 的核心承诺。
//
// 设计取舍：
//   - 上层 API 围绕 Connected → Send → Subscribe 三件事；
//   - 消息可靠性：本地 Store 先做幂等缓存，FKDeliver 到达再写一次（ID 相同则 no-op）；
//     这样 Client 可以在断网时把"我想说的话"塞进 store，离线发送靠外层 Job。
//   - 重连 / 补发由调用方在更上层做（M2 起 Hub 提供 SDK）；
//     M1 客户端暴露 `History(...)` 让测试能直接驱动补发。
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// cliLog 是 Client 收发与状态机的 logger。
var cliLog = logging.New("client")

// ConnectOptions 是 Connect 的可选项。
type ConnectOptions struct {
	// ResumeFrom 决定本次连接之后 Hub 应补发的 ServerSeq 起点。
	// 0 = 完整重放；>0 = 仅补发此值之后的消息。
	ResumeFrom uint64
	// RequestHistory 控制是否在 Hello 之后主动发一次 FKHistoryReq 请求。
	// M1 默认开。生产可关闭，让 Hub 自带此逻辑。
	RequestHistory bool
	// HistoryLimit 是首屏拉取条数；<=0 默认 50。
	HistoryLimit int
	// HistoryWaitTimeout 限定第一次 FKHistoryReq 后等待响应的最长时长。
	// 超过则放弃「先把 history 排到 deliver 之前」的有序保证，按 FKDeliver 到达顺序直接 publish。
	// <=0 时默认 5s；0 不应被理解为立即放弃——它会把 FKDeliver 当成主线，是常规下不应选的语义。
	HistoryWaitTimeout time.Duration
}

// defaultHistoryWaitTimeout 是 HistoryWaitTimeout 的兜底值。
const defaultHistoryWaitTimeout = 5 * time.Second

// Client 是面向业务的高层 API。
type Client struct {
	hello protocol.Hello
	conn  core.Conn
	store core.Store
	bus   core.EventBus

	// pumpCtx / pumpCancel 是 readPump 自己的生命周期。
	//
	// 与 Connect 传入的 ctx 解耦：调用方传入的 ctx 只用于 hello/history req send
	// 的写超时；readPump 跑在 pumpCtx 上，由 Client.Close() 统一取消。
	//
	// 这是 B 方案的核心：cmd/tui 的 dialSession 会 defer cancel(dialCtx)，
	// 旧实现把这个 ctx 一路传给 readPump → dialSession 一返回 readPump 就死，
	// 客户端收不到任何服务端帧（FKHistoryResp / FKDeliver），TUI 顶上变 offline
	// 但 sender 仍能"成功" send（hub 真收到）—— 消息广播出去但客户端读不出来，
	// 现象就是"发消息是空的"。
	pumpCtx    context.Context
	pumpCancel context.CancelFunc

	// seen 跟踪已发射 EventMessage 的消息 ID，防止：
	//   1. 同一个消息先以 FKDeliver 到达、后以 FKHistoryResp 补发时，bus 上出现两次事件；
	//   2. 重连场景下同一历史区间的消息二次入站。
	// 内存压力可忽略：每个 Client 进程内最多见过 N 条消息；定时 GC 待 M2 引入。
	seenMu sync.Mutex
	seen   map[string]struct{}

	// histWait 是 FetchHistory 的同步等待点：非 nil 时，下一个到达的
	// FKHistoryResp 会被送给该 channel，且消息只写 Store、不发布 EventMessage。
	// 分页结果由调用方自行渲染——否则旧消息会经实时通道（SSE）当新消息
	// 重复追加到界面底部。nil 时（Connect 的 catch-up 补发）走原发布路径。
	histMu   sync.Mutex
	histWait chan protocol.HistoryResponse

	// awaitingHistory 为 true 表示「FKHistoryReq 已发出、还没拿到响应」期间，
	// 用来在 race window 里为 FKDeliver 排队——详见 deliverMessage / flushPendingDeliver。
	// 这是一道 catch-up 期间的事件顺序护栏：FKDeliver 不能跑到 FKHistoryResp 的消息前面，
	// 否则 bus 上会出现 [M2, M1] 这种乱序事件（hub 端广播是异步、跨 goroutine 的）。
	awaitingHistory atomic.Bool
	// pendingDeliver 是 catch-up 窗口里到达的 FKDeliver，等 FKHistoryResp 一来再 flush。
	// 内部字段都在 pendingMu 保护下读写，包含与 timer goroutine (forceFlushPending) 的同步。
	pendingMu      sync.Mutex
	pendingDeliver []protocol.StoredMessage
	lastHistorySeq uint64

	// peers 是当前在线成员名单（M7.2），按 DeviceID 索引。
	// Hub 握手后发 roster + 上下线广播，dispatch 里 upsert；offline 直接删除。
	// 与消息不同：presence 是瞬时状态、不持久化，重连后 hub 重发 roster 自然重建。
	// Web 多 Tab 共享同一个 Client，新 Session 订阅 EventBus 时 roster 事件早已
	// 过去、无处可查，所以首屏成员列表从这里取快照。
	peersMu sync.RWMutex
	peers   map[string]protocol.Presence

	// typers 是「正在输入」的设备表（M7.3），按 DeviceID 索引最后收到的
	// 时间。与 peers 同属瞬时连接状态、不持久化；惰性过期——Typing() 快照
	// 顺带清掉 typingTTL 前的旧条目，无需后台定时器。
	typers map[string]typingEntry

	closed atomic.Bool
	done   chan struct{}
}

// typingEntry 是一台设备「正在输入」的记录（M7.3）。
type typingEntry struct {
	user string
	at   time.Time
}

// typingTTL 是 typing 状态在 Client 快照里的保留时长；与 UI 层的
// 展示有效期同量级（节流间隔的 2 倍），容忍丢帧但不长时间残留。
const typingTTL = 6 * time.Second

// New 用已建立的 Conn 构造 Client（Conn 由 Transport.Dial 或服务端 Accept 给出）。
func New(hello protocol.Hello, conn core.Conn, store core.Store, bus core.EventBus) *Client {
	return &Client{
		hello:  hello,
		conn:   conn,
		store:  store,
		bus:    bus,
		seen:   make(map[string]struct{}),
		peers:  make(map[string]protocol.Presence),
		typers: make(map[string]typingEntry),
		done:   make(chan struct{}),
	}
}

// Connect 握手 + (可选)历史补发，然后启动 readPump。
//
// ctx 的作用范围仅限本次调用内 hello/history req 的 send 写超时。
// readPump 跑在 Client 自己的 pumpCtx 上（Connect 一起、Close 一关），
// 不受 ctx 取消影响——否则 cmd/tui dialSession defer cancel(dialCtx)
// 会把 readPump 一起干掉，客户端收不到任何服务端帧。
//
// 必须在 conn 已被 Transport.Dial 返回后调用。
func (c *Client) Connect(ctx context.Context, opts ConnectOptions) error {
	if opts.RequestHistory && opts.HistoryLimit <= 0 {
		opts.HistoryLimit = 50
	}
	if opts.HistoryWaitTimeout <= 0 {
		opts.HistoryWaitTimeout = defaultHistoryWaitTimeout
	}

	// 起 pumpCtx：readPump 的生命周期由 Client 自己管理，与调用方 ctx 解耦。
	// 即使 ctx 在 Connect 返回后被取消（典型场景：cmd/tui 的 defer dialCancel()），
	// readPump 也会继续跑，直到 Client.Close()。
	c.pumpCtx, c.pumpCancel = context.WithCancel(context.Background())

	// 1. 发送 Hello（带 ResumeFrom）
	hello := c.hello
	hello.ResumeFrom = opts.ResumeFrom
	payload, err := json.Marshal(hello)
	if err != nil {
		c.pumpCancel()
		return fmt.Errorf("marshal hello: %w", err)
	}
	cliLog.Info("send hello", "user", hello.UserID, "device", hello.DeviceID, "resume_from", hello.ResumeFrom)
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKHello, Payload: payload}); err != nil {
		c.pumpCancel()
		return fmt.Errorf("send hello: %w", err)
	}

	// 2. 主动请求历史（若有）
	if opts.RequestHistory {
		c.awaitingHistory.Store(true)
		req := protocol.HistoryRequest{After: opts.ResumeFrom, Limit: opts.HistoryLimit}
		reqPayload, _ := json.Marshal(req)
		cliLog.Debug("send history req", "after", opts.ResumeFrom, "limit", opts.HistoryLimit)
		if err := c.conn.Send(ctx, protocol.Frame{
			Kind:    protocol.FKHistoryReq,
			Payload: reqPayload,
		}); err != nil {
			c.awaitingHistory.Store(false)
			c.pumpCancel()
			return fmt.Errorf("send history req: %w", err)
		}
		// 兜底超时：Hub 没响应也不能让 buffer 一直挂起；超时后按到达顺序直接 publish。
		// 用闭包捕获 opts 的超时；Close() 也会强制清（见 forceFlushPending），保证 quit 路径无残留。
		time.AfterFunc(opts.HistoryWaitTimeout, c.forceFlushPending)
	}

	go c.readPump(c.pumpCtx) //nolint:contextcheck // pumpCtx 由 Client 自身管理，Client 没有父 ctx
	return nil
}

// readPump 把服务端帧分发到 EventBus 与 Store。
//
// ctx 来源：Connect 起的 pumpCtx（Client 内部管理），与调用方传入的 dialCtx 解耦。
// 退出条件：pumpCancel 被调（Close 路径）或 conn 被关（对端断开）。
func (c *Client) readPump(ctx context.Context) {
	defer close(c.done)
	cliLog.Debug("readPump started")
	for {
		f, err := c.conn.Recv(ctx)
		if err != nil {
			if !c.closed.Load() {
				cliLog.Error("readPump recv failed", "err", err)
				c.bus.Publish(core.Event{
					Kind: core.EventState,
					State: &core.StateInfo{
						Connected: false,
						Err:       err,
					},
				})
			} else {
				cliLog.Debug("readPump exited after close")
			}
			return
		}
		c.dispatch(ctx, f)
	}
}

// dispatch 根据帧类型分发。
func (c *Client) dispatch(ctx context.Context, f protocol.Frame) {
	cliLog.Debug("dispatch", "kind", f.Kind, "len", len(f.Payload))
	switch f.Kind {
	case protocol.FKDeliver:
		var msg protocol.StoredMessage
		if err := json.Unmarshal(f.Payload, &msg); err != nil {
			cliLog.Error("unmarshal FKDeliver failed", "err", err)
			return
		}
		// 幂等持久化，upsert-by-ID 保证 ServerSeq 由 Hub 补齐；
		// 入事件总线走 deliverMessage 入口，受 catch-up 缓冲护栏约束。
		_ = c.store.AppendMessage(ctx, msg)
		c.deliverMessage(&msg)

	case protocol.FKHistoryResp:
		var resp protocol.HistoryResponse
		if err := json.Unmarshal(f.Payload, &resp); err != nil {
			cliLog.Error("unmarshal FKHistoryResp failed", "err", err)
			return
		}
		cliLog.Debug("history resp received", "count", len(resp.Messages))

		// FetchHistory 的分页响应：消息只落 Store，不发布事件。
		// 调用方拿到 resp 自行渲染（Web 端「加载更多」把片段插到列表顶部）；
		// 若走下面的 publishMessageOnce，SSE 实时通道会把这些旧消息当新消息
		// beforeend 重复追加到界面底部。
		c.histMu.Lock()
		wait := c.histWait
		c.histWait = nil
		c.histMu.Unlock()
		if wait != nil {
			for i := range resp.Messages {
				_ = c.store.AppendMessage(ctx, resp.Messages[i])
			}
			wait <- resp
			return
		}

		// history resp 的内容必须按 Hub 给的顺序直送 publishMessageOnce，
		// 不能走 deliverMessage——后者在 awaitingHistory 时会全部进 buffer，
		// 而 buffer 在 flushPendingDeliver 里又会被 lastHistorySeq 过滤掉，
		// 就把 history 自己的消息也丢了。
		// 这里走直送：先按 Hub 给的升序把 history 推入事件总线，期间累计最大 ServerSeq；
		// 然后 flushPendingDeliver 把 catch-up 窗口里抢着到达、且比 history 更新的 FKDeliver 补发。
		for i := range resp.Messages {
			m := resp.Messages[i]
			_ = c.store.AppendMessage(ctx, m)
			c.publishMessageOnce(&m)
			// 通过 pendingMu 保护下写入 lastHistorySeq；
			// flushPendingDeliver 紧随其后读，forceFlushPending 的 timer goroutine 也通过同一把锁读，
			// 这样不依赖 happens-before 也能让 race detector 通过。
			c.setLastHistorySeq(m.ServerSeq)
		}
		c.flushPendingDeliver()
		// 末尾通知"已同步"，调用方可挂回调触发 UI 刷新
		c.bus.Publish(core.Event{
			Kind:  core.EventState,
			State: &core.StateInfo{Connected: true},
		})

	case protocol.FKError:
		var e protocol.ErrorPayload
		_ = json.Unmarshal(f.Payload, &e)
		cliLog.Error("server error", "code", e.Code, "msg", e.Message)
		c.bus.Publish(core.Event{
			Kind: core.EventState,
			State: &core.StateInfo{
				Connected: false,
				Err:       fmt.Errorf("server error: code=%d msg=%s", e.Code, e.Message),
			},
		})
		_ = c.conn.Close() // 收到 error 后主动断开，让 readPump 退出

	case protocol.FKPong:
		// 心跳应答，仅 log-and-drop；M2 起将暴露给上层做延迟统计。
		cliLog.Debug("pong received")

	case protocol.FKPresence:
		// M7.2：设备上下线通知。Hub 在握手后发在线名单 + 广播上下线。
		// 先更新 Client 自己的名单快照（Peers() 供 Web 首屏渲染），
		// 再发布事件让 UI 实时刷新；顺序保证订阅者收到事件时快照已最新。
		var pr protocol.Presence
		if err := json.Unmarshal(f.Payload, &pr); err != nil {
			cliLog.Error("unmarshal FKPresence failed", "err", err)
			return
		}
		c.applyPresence(pr)
		c.bus.Publish(core.Event{
			Kind:     core.EventPresence,
			Presence: &pr,
		})

	case protocol.FKTyping:
		// M7.3：他人正在输入。负载是 Hub 盖戳后的身份；自己发的 typing
		// 不会回显（Hub 排除发送者），这里收到的一律是别人。
		// 先更新 Client 的 typing 快照（Typing() 供 Web SSE 帧全量重渲），
		// 再发布事件让 UI 即时刷新。
		var ty protocol.Typing
		if err := json.Unmarshal(f.Payload, &ty); err != nil {
			cliLog.Error("unmarshal FKTyping failed", "err", err)
			return
		}
		if ty.DeviceID == "" {
			return
		}
		c.applyTyping(ty)
		c.bus.Publish(core.Event{
			Kind:   core.EventTyping,
			Typing: &ty,
		})

	default:
		// 其它帧不在 M1 范围内，静默丢弃。
	}
}

// applyPresence 把一条 Presence 更新进内部名单（M7.2）。
// online 按 DeviceID upsert；offline 直接删除——成员列表只展示在线设备。
// DeviceID 为空的异常帧忽略，避免产生无法归属的条目。
func (c *Client) applyPresence(pr protocol.Presence) {
	if pr.DeviceID == "" {
		return
	}
	c.peersMu.Lock()
	defer c.peersMu.Unlock()
	if pr.Online {
		c.peers[pr.DeviceID] = pr
	} else {
		delete(c.peers, pr.DeviceID)
		// 设备离线，它的「正在输入」一并清除。
		delete(c.typers, pr.DeviceID)
	}
}

// applyTyping 记录/刷新一台设备的「正在输入」时间（M7.3）。
func (c *Client) applyTyping(ty protocol.Typing) {
	c.peersMu.Lock()
	defer c.peersMu.Unlock()
	c.typers[ty.DeviceID] = typingEntry{user: ty.UserID, at: time.Now()}
}

// Peers 返回当前在线成员名单快照，按 DeviceID 升序（结果稳定，便于渲染与测试）。
//
// 数据源是 Client 内部维护的 presence 状态（Hub roster + 上下线广播），
// 不是事件总线——新订阅者（如 Web 新开 Tab 的 Session）错过历史事件后
// 仍能从这里拿到首屏名单。
func (c *Client) Peers() []protocol.Presence {
	c.peersMu.RLock()
	out := make([]protocol.Presence, 0, len(c.peers))
	for _, pr := range c.peers {
		out = append(out, pr)
	}
	c.peersMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

// Typing 返回当前正在输入的成员快照（M7.3），按 UserID 去重升序。
//
// 惰性过期：顺带清掉 typingTTL 前的条目，无需后台定时器。typing 是
// 瞬时状态，Web SSE 帧每次以全量快照重渲；TUI 自己维护事件驱动的
// 过期 Tick，不读本快照。
func (c *Client) Typing() []protocol.Typing {
	now := time.Now()
	c.peersMu.Lock()
	for dev, e := range c.typers {
		if now.Sub(e.at) >= typingTTL {
			delete(c.typers, dev)
		}
	}
	seen := make(map[string]struct{}, len(c.typers))
	out := make([]protocol.Typing, 0, len(c.typers))
	for dev, e := range c.typers {
		if _, dup := seen[e.user]; dup {
			continue
		}
		seen[e.user] = struct{}{}
		out = append(out, protocol.Typing{UserID: e.user, DeviceID: dev})
	}
	c.peersMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out
}

// publishMessageOnce 防止同一 ID 的消息重复发射到 EventBus。
// 返回 false 表示已见过，应当跳过。
func (c *Client) publishMessageOnce(msg *protocol.StoredMessage) bool {
	if msg == nil || msg.ID == "" {
		return false
	}
	c.seenMu.Lock()
	if _, ok := c.seen[msg.ID]; ok {
		c.seenMu.Unlock()
		return false
	}
	c.seen[msg.ID] = struct{}{}
	c.seenMu.Unlock()

	m := *msg // 拷贝，防止调用方修改（指针）
	c.bus.Publish(core.Event{
		Kind:           core.EventMessage,
		ConversationID: m.ConversationID,
		Message:        &m,
	})
	return true
}

// deliverMessage 是 FKDeliver / FKHistoryResp 共用的「入事件总线」入口。
//
// 在 Connect 请求了 history 的情况下，catch-up 窗口里到达的 FKDeliver 需要排队：
// hub 的 broadcast 是异步的（跨 goroutine），FKHistoryReq 与后续 aliceB 发送的 FKMessage
// 之间存在真实的 race，新连上来的 bobB 可能先收到「刚被广播出去的 M2」，
// 后收到「FKHistoryResp 里的 [M1, M2]」，结果事件总线出现 [M2, M1] 这种乱序。
// 这里把 race window 里的 FKDeliver 先缓存；FKHistoryResp 到达后 flush 一次，
// 保证事件总线上的消息序列在 ServerSeq 上单调递增。
func (c *Client) deliverMessage(msg *protocol.StoredMessage) {
	if msg == nil {
		return
	}
	if c.awaitingHistory.Load() {
		c.pendingMu.Lock()
		c.pendingDeliver = append(c.pendingDeliver, *msg)
		c.pendingMu.Unlock()
		return
	}
	c.publishMessageOnce(msg)
}

// flushPendingDeliver 在 FKHistoryResp 收完时被调用一次：
//  1. 把 await 期间收集到的 FKDeliver 按"是否已被 history 覆盖"过滤；
//  2. 把剩余的、确实比 history 更新的 FKDeliver 顺序补发到事件总线；
//  3. 清掉 awaitingHistory，让后续 FKDeliver 走直送路径。
//
// publishMessageOnce 的 seen 集合已经能拦住"FKHistoryResp + 重复 FKDeliver"的重复事件；
// 这里只解决"乱序"。flush 不排序——pendingDeliver 按到达顺序追加，
// hub 的 broadcast 单条是同步顺序发出，单条 FKDeliver 内部已是有序；
// 但 race window 里 M2 的 deliver 跑到 history resp 之前是站得住的，
// 所以需要这条护栏保证总线上"history 先 → race 来的 deliver 后"。
func (c *Client) flushPendingDeliver() {
	if !c.awaitingHistory.CompareAndSwap(true, false) {
		// 已经被超时路径或 Close 兜底清过；不重复 flush。
		return
	}
	c.pendingMu.Lock()
	pending := c.pendingDeliver
	c.pendingDeliver = nil
	c.pendingMu.Unlock()
	last := c.peekLastHistorySeq()

	for i := range pending {
		m := &pending[i]
		// ServerSeq <= lastHistorySeq 的已经在 FKHistoryResp 里走过 publishMessageOnce，
		// 这里再走也只是命中 seen 集合，但省去一次哈希查找更稳。
		if m.ServerSeq != 0 && m.ServerSeq <= last {
			continue
		}
		c.publishMessageOnce(m)
	}
}

// forceFlushPending 是 Connect 设的兜底超时回调。timeout 后无论是否拿到 FKHistoryResp，
// 都强制走"按到达顺序直送"——这是降级语义，事件顺序有可能非严格按 ServerSeq，
// 但不能因为 hub 一次没回就把整个客户端卡住。
func (c *Client) forceFlushPending() {
	if !c.awaitingHistory.CompareAndSwap(true, false) {
		return
	}
	c.pendingMu.Lock()
	pending := c.pendingDeliver
	c.pendingDeliver = nil
	c.pendingMu.Unlock()
	for i := range pending {
		c.publishMessageOnce(&pending[i])
	}
}

// peekLastHistorySeq / setLastHistorySeq 把 lastHistorySeq 的访问串行化到 pendingMu 上：
// setLastHistorySeq 由 dispatch FKHistoryResp goroutine 写，
// peekLastHistorySeq 由 flushPendingDeliver / forceFlushPending（timer goroutine 也在内）读，
// 跨 goroutine 读写不加锁 race detector 会报警。
func (c *Client) peekLastHistorySeq() uint64 {
	c.pendingMu.Lock()
	v := c.lastHistorySeq
	c.pendingMu.Unlock()
	return v
}

func (c *Client) setLastHistorySeq(seq uint64) {
	c.pendingMu.Lock()
	if seq > c.lastHistorySeq {
		c.lastHistorySeq = seq
	}
	c.pendingMu.Unlock()
}

// SendMessage 发出一条消息。Client 立即返回（不阻塞等回执）。
// 服务端之后会通过 FKDeliver 回一份，readPump 负责持久化与事件发射。
func (c *Client) SendMessage(ctx context.Context, convID, body string) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	nonce := newNonce()
	msg := protocol.StoredMessage{
		// Hub 重写 ID；本地的"想去发"的 ID 只用于客户端侧 print/debug。
		ID:             "local-" + nonce,
		ClientNonce:    nonce,
		ConversationID: convID,
		SenderUserID:   c.hello.UserID,
		SenderDeviceID: c.hello.DeviceID,
		Body:           body,
		CreatedAt:      time.Now().UnixMilli(),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	cliLog.Debug("send FKMessage", "conv", convID, "len", len(body))
	if err := c.conn.Send(ctx, protocol.Frame{
		Kind:    protocol.FKMessage,
		Payload: payload,
	}); err != nil {
		cliLog.Error("send FKMessage failed", "conv", convID, "err", err)
		return err
	}
	// 乐观本地缓存（FKDeliver 到达时同 ID upsert，会把 ServerSeq 从 0 更新成 Hub 分配值）。
	// 错误仍然忽略：本地落库失败不应阻断发消息。
	_ = c.store.AppendMessage(ctx, msg)
	return nil
}

// SendRead 标记某会话已读。Client 会同时：
//   - 写本地 store 的 cursor（方便 GetCursor）
//   - 给服务端发 FKRead 帧（让 Hub 也更新）
func (c *Client) SendRead(ctx context.Context, convID string, serverSeq uint64) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	payload, _ := json.Marshal(protocol.Read{ConversationID: convID, ServerSeq: serverSeq})
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKRead, Payload: payload}); err != nil {
		return err
	}
	return c.store.SetCursor(ctx, c.hello.DeviceID, convID, serverSeq)
}

// SendTyping 上发「正在输入」提示（M7.3）。负载为空——身份由 Hub 按
// 连接注册表盖戳。瞬时提示 best-effort：发送失败只 Debug 日志、不报错，
// 调用方（UI 层）负责节流（建议 ≥3s 一次），避免每个按键都打帧。
func (c *Client) SendTyping(ctx context.Context) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKTyping}); err != nil {
		cliLog.Debug("send FKTyping failed", "err", err)
		return err
	}
	return nil
}

// History 直接读本地 Store。已含已读游标过滤在调用方做。
func (c *Client) History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error) {
	return c.store.History(ctx, convID, after, limit)
}

// FetchHistory 向 Hub 请求一段历史并同步等待响应。
//
//   - before>0：向更早翻页（ServerSeq 严格小于 before 的最晚一批，升序）；
//   - before==0 且 after>0：增量补发（严格大于 after 的最早一批）。
//
// 返回的消息已写入本地 Store，但**不会**发布 EventMessage 事件——
// 分页结果由调用方自行渲染（Web「加载更多」把片段插到列表顶部），
// 避免旧消息经 SSE 实时通道当新消息重复追加。
//
// 同一 Client 同时只允许一个 FetchHistory 在途（UI 上也只有一个分页按钮）。
func (c *Client) FetchHistory(ctx context.Context, convID string, after, before uint64, limit int) (protocol.HistoryResponse, error) {
	if c.closed.Load() {
		return protocol.HistoryResponse{}, core.ErrClosed
	}
	ch := make(chan protocol.HistoryResponse, 1)
	c.histMu.Lock()
	if c.histWait != nil {
		c.histMu.Unlock()
		return protocol.HistoryResponse{}, fmt.Errorf("fetch history already in flight")
	}
	c.histWait = ch
	c.histMu.Unlock()
	defer func() {
		c.histMu.Lock()
		c.histWait = nil
		c.histMu.Unlock()
	}()

	req := protocol.HistoryRequest{
		ConversationIDs: []string{convID},
		After:           after,
		Before:          before,
		Limit:           limit,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return protocol.HistoryResponse{}, fmt.Errorf("marshal history req: %w", err)
	}
	cliLog.Debug("send fetch history req", "conv", convID, "after", after, "before", before, "limit", limit)
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKHistoryReq, Payload: payload}); err != nil {
		return protocol.HistoryResponse{}, fmt.Errorf("send history req: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return protocol.HistoryResponse{}, ctx.Err()
	case <-c.done:
		return protocol.HistoryResponse{}, core.ErrClosed
	}
}

// Cursor 读取该设备在某会话的已读游标。
func (c *Client) Cursor(ctx context.Context, convID string) (uint64, error) {
	return c.store.GetCursor(ctx, c.hello.DeviceID, convID)
}

// Subscribe 暴露 EventBus 订阅。buf 是订阅者 channel 缓冲。
func (c *Client) Subscribe(buf int) core.Subscription {
	return c.bus.Subscribe(buf)
}

// Done 在 readPump 退出时关闭（即连接断开）。
func (c *Client) Done() <-chan struct{} { return c.done }

// Close 幂等关闭连接。返回 readPump 退出所花的时间。
//
// 关闭前先把 awaitingHistory 兜底清掉，避免 catch-up 缓冲里的待发消息因为超时回调
// 撞上一个正在 Close 的 Client（forceFlushPending 与 flushPendingDeliver 都按
// awaitingHistory 的 CompareAndSwap 互斥，多次调用安全）。
//
// 关闭顺序：pumpCancel → forceFlushPending → conn.Close → 等 done。
// 先取消 pumpCtx 让 readPump 走 ctx-canceled 路径退出，再关 conn。
// 反过来 readPump 也会退出，但走的是 conn-closed 路径，错误语义不同（"对端断了" vs "本地关了"）。
// c.closed=true 已经屏蔽了那条路径上的 EventState 误发，但顺序仍按"主动取消在前"更直观。
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	cliLog.Info("close client")
	if c.pumpCancel != nil {
		c.pumpCancel()
	}
	c.forceFlushPending()
	err := c.conn.Close()
	<-c.done
	return err
}

// newNonce 生成形如 "n-<unixnano>-<hexcounter>" 的客户端 nonce。
//   - 时间戳给 Hub 用来断网时的"先后顺序"判断，
//   - 单调计数器给本地的"瞬时重试"用。
var nonceCounter uint64

func newNonce() string {
	return fmt.Sprintf("n-%d-%016x", time.Now().UnixNano(), atomic.AddUint64(&nonceCounter, 1))
}
