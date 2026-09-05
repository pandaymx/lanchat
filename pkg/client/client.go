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
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

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

	// seen 跟踪已发射 EventMessage 的消息 ID，防止：
	//   1. 同一个消息先以 FKDeliver 到达、后以 FKHistoryResp 补发时，bus 上出现两次事件；
	//   2. 重连场景下同一历史区间的消息二次入站。
	// 内存压力可控：每个 Client 进程内最多见过 N 条消息；定时 GC 待 M2 引入。
	seenMu sync.Mutex
	seen   map[string]struct{}

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

	closed atomic.Bool
	done   chan struct{}
}

// New 用已建立的 Conn 构造 Client（Conn 由 Transport.Dial 或服务端 Accept 给出）。
func New(hello protocol.Hello, conn core.Conn, store core.Store, bus core.EventBus) *Client {
	return &Client{
		hello: hello,
		conn:  conn,
		store: store,
		bus:   bus,
		seen:  make(map[string]struct{}),
		done:  make(chan struct{}),
	}
}

// Connect 握手 + (可选)历史补发，然后启动 readPump。
// 必须在 conn 已被 Transport.Dial 返回后调用。
func (c *Client) Connect(ctx context.Context, opts ConnectOptions) error {
	if opts.RequestHistory && opts.HistoryLimit <= 0 {
		opts.HistoryLimit = 50
	}
	if opts.HistoryWaitTimeout <= 0 {
		opts.HistoryWaitTimeout = defaultHistoryWaitTimeout
	}
	// 1. 发送 Hello（带 ResumeFrom）
	hello := c.hello
	hello.ResumeFrom = opts.ResumeFrom
	payload, err := json.Marshal(hello)
	if err != nil {
		return fmt.Errorf("marshal hello: %w", err)
	}
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKHello, Payload: payload}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	// 2. 主动请求历史（若有）
	if opts.RequestHistory {
		c.awaitingHistory.Store(true)
		req := protocol.HistoryRequest{After: opts.ResumeFrom, Limit: opts.HistoryLimit}
		reqPayload, _ := json.Marshal(req)
		if err := c.conn.Send(ctx, protocol.Frame{
			Kind:    protocol.FKHistoryReq,
			Payload: reqPayload,
		}); err != nil {
			c.awaitingHistory.Store(false)
			return fmt.Errorf("send history req: %w", err)
		}
		// 兜底超时：Hub 没响应也不能让 buffer 一直挂起；超时后按到达顺序直接 publish。
		// 用闭包捕获 opts 的超时；Close() 也会强制清（见 forceFlushPending），保证 quit 路径无残留。
		time.AfterFunc(opts.HistoryWaitTimeout, c.forceFlushPending)
	}

	go c.readPump(ctx)
	return nil
}

// readPump 把服务端帧分发到 EventBus 与 Store。
//
// ctx 来源：通常 Connect 传入的 ctx；其取消后读循环退出。
// Close() 通过关闭底 conn 让 Recv 立即返回错误，也能让 readPump 退出。
func (c *Client) readPump(ctx context.Context) {
	defer close(c.done)
	for {
		f, err := c.conn.Recv(ctx)
		if err != nil {
			if !c.closed.Load() {
				c.bus.Publish(core.Event{
					Kind: core.EventState,
					State: &core.StateInfo{
						Connected: false,
						Err:       err,
					},
				})
			}
			return
		}
		c.dispatch(ctx, f)
	}
}

// dispatch 根据帧类型分发。
func (c *Client) dispatch(ctx context.Context, f protocol.Frame) {
	switch f.Kind {
	case protocol.FKDeliver:
		var msg protocol.StoredMessage
		if err := json.Unmarshal(f.Payload, &msg); err != nil {
			return
		}
		// 幂等持久化，upsert-by-ID 保证 ServerSeq 由 Hub 补齐；
		// 入事件总线走 deliverMessage 入口，受 catch-up 缓冲护栏约束。
		_ = c.store.AppendMessage(ctx, msg)
		c.deliverMessage(&msg)

	case protocol.FKHistoryResp:
		var resp protocol.HistoryResponse
		if err := json.Unmarshal(f.Payload, &resp); err != nil {
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

	default:
		// 其它帧不在 M1 范围内，静默丢弃。
	}
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
	if err := c.conn.Send(ctx, protocol.Frame{
		Kind:    protocol.FKMessage,
		Payload: payload,
	}); err != nil {
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

// History 直接读本地 Store。已含已读游标过滤在调用方做。
func (c *Client) History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error) {
	return c.store.History(ctx, convID, after, limit)
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
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
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
