// Package core 定义 lanchat 的核心抽象接口。
//
// 这里只有"契约"，没有任何实现。具体实现分布在：
//   - pkg/transport/fake      —— 同进程内内存 Transport（M1 测试用）
//   - pkg/transport/ws        —— WebSocket Transport（M2 Hub 服务端/客户端共用）
//   - pkg/store/memory        —— 内存 Store（M1 测试用 / single-node 模式备选）
//   - pkg/store/sqlite        —— SQLite Store（M2 起 Hub 默认）
//   - pkg/event               —— 进程内 EventBus
//
// 设计原则（来自 ADR-002 与 ADR-008）：
//   - 接口层不引入任何网络协议细节（import pkg/protocol 是允许的，因为它本身就是协议中立）
//   - Transport 负责"字节双向流动"，不负责语义
//   - Store 负责"持久化与查询"，不负责网络
//   - EventBus 负责"本地组件通信"，不涉及远程
//   - Client（pkg/client，由 Transport+Store+EventBus 组合）是面向业务的高层 API
package core

import (
	"context"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// Conn 是一条已建立的逻辑连接。
//
// 它的语义是：**双向有序字节流**。但顺序性保证仅在每个 Conn 内（多条 Conn 之间不保证顺序）。
//
// 生命周期：
//   - Client 侧由 Transport.Dial 返回
//   - Hub 侧由 Transport.Listen 通过回调给出
//   - 任何一方调用 Close 后，Recv 会返回 io.EOF，Send 会返回 ErrClosed
//
// 发送与接收都是阻塞的，调用者负责传入合理的 ctx 来取消。
type Conn interface {
	Send(ctx context.Context, f protocol.Frame) error
	Recv(ctx context.Context) (protocol.Frame, error)
	Close() error
	Closed() <-chan struct{} // 远端/本地关闭后关闭
	LocalDeviceID() string
}

// Transport 抽象"连接的创建"。
//
// 有两种角色：
//   - Client 角色：调用 Dial(ctx, target, hello) → Conn
//   - Server 角色：调用 Listen(ctx, addr, onConn) → 一直运行直到 ctx 取消
//
// 同一个实现可以同时支持两种角色（FakeTransport / WebSocket 都行）。
type Transport interface {
	Dial(ctx context.Context, target string, hello protocol.Hello) (Conn, error)
	Listen(ctx context.Context, addr string, onConn func(Conn, protocol.Hello) error) error
}

// EventKind 是 EventBus 顶层事件分类。
//
// 注意：FrameKind（wire 层）和 EventKind（本地 EventBus 层）完全不同。
//   - FrameKind 关心的是"线缆上这一坨字节说的是什么"
//   - EventKind 关心的是"业务订阅者该如何处理"
//
// 参考 pkg/client 的 readPump：把 FKDeliver frame → EventMessage event。
type EventKind int

const (
	// EventMessage 新消息已落库并可读。
	EventMessage EventKind = iota + 1
	// EventRead 某会话已读游标推进（你自己或别人）。
	EventRead
	// EventPresence 设备上线/下线。
	EventPresence
	// EventTyping 某人正在输入（轻度提示，不持久化）。
	EventTyping
	// EventState 本地连接状态变化（连接/已断/重连中）—— 不是线缆上的事件。
	EventState
)

// String 用于日志与 CLI 渲染。wire 协议里见 protocol.FrameKind.String。
func (k EventKind) String() string {
	switch k {
	case EventMessage:
		return "message"
	case EventRead:
		return "read"
	case EventPresence:
		return "presence"
	case EventTyping:
		return "typing"
	case EventState:
		return "state"
	default:
		return "unknown"
	}
}

// Event 是 EventBus 上的事件载荷。
//
// 用单结构体 + sum-kind tags 而不是 interface{}, 是因为：
//   - 简单、golang 友好，无需 reflection；
//   - 调用者做类型断言时也有静态提示（switch k 就过得了 testify 类型检查）。
type Event struct {
	Kind           EventKind
	ConversationID string
	Message        *protocol.StoredMessage // 当 Kind == EventMessage
	Read           *protocol.ReadCursor    // 当 Kind == EventRead
	Presence       *protocol.Presence      // 当 Kind == EventPresence
	State          *StateInfo              // 当 Kind == EventState
}

// StateInfo 是 EventState 的载荷。
type StateInfo struct {
	Connected bool
	Err       error
}

// EventBus 是进程内事件总线。
//
// 多个订阅者各自一条 channel，避免一个慢消费者阻塞其他订阅者。
// 满了就丢（Send 端不阻塞）—— EventBus 不背可靠投递的债，可靠性由 Store 和 Conn 重传保证。
type EventBus interface {
	Publish(e Event)
	Subscribe(buf int) Subscription
}

// Subscription 由 EventBus.Subscribe 返回，调用方负责 Close。
type Subscription interface {
	C() <-chan Event
	Close() error
}

// Store 是持久化抽象。
//
// 实现可以是内存（M1 测试 / single-node）或 SQLite（M2 起 Hub 默认）。
// 设计上强制"先落库后投递"——Send 消息之后，下一次 History 必然能查到。
//
// 该接口只规定了**跨进程共用的最小集**。Hub 内部还会用到额外的内部状态
// （如每设备活跃连接表），那些放在 pkg/internal/hubstate，不属于 pkg/core。
type Store interface {
	SaveUser(ctx context.Context, u protocol.User) error
	GetUser(ctx context.Context, id string) (protocol.User, error)

	SaveDevice(ctx context.Context, d protocol.Device) error
	GetDevice(ctx context.Context, id string) (protocol.Device, error)

	SaveConversation(ctx context.Context, c protocol.Conversation) error
	GetConversation(ctx context.Context, id string) (protocol.Conversation, error)

	// AppendMessage 由 Hub 在接收 FKMessage 后调用：
	//   - Hub 分配 ServerSeq；
	//   - Hub 设置 CreatedAt 为接收时刻；
	//   - Store 持久化。
	// M1 Store 不分配序号，调用方负责传入；M2 SQLite Store 会自动分配。
	AppendMessage(ctx context.Context, m protocol.StoredMessage) error

	// History 返回 (convID, after) 之后、按 ServerSeq 升序的消息，最多 limit 条。
	// limit<=0 表示无限（实现可设上限防 OOM）。
	History(ctx context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error)

	// SetCursor 与 GetCursor 共同维护 per-device 阅读游标。
	// 这是多设备读同步的关键（参见 ADR-008）。
	SetCursor(ctx context.Context, deviceID, convID string, seq uint64) error
	GetCursor(ctx context.Context, deviceID, convID string) (uint64, error)

	Close() error
}
