package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// DefaultConversationID 是 M3 单会话阶段使用的会话 ID。
//
// Hub 侧按 ConversationID 分桶存放历史（见 pkg/hubstate/history.go），
// 会话不需要预先注册，因此这里用一个固定常量即可，无需建会话流程。
// M7 引入群聊/频道后由上层传入真实会话 ID 替换。
const DefaultConversationID = "lobby"

// eventBuf 是订阅通道缓冲。EventBus 满则丢弃，给足缓冲降低丢事件概率。
const eventBuf = 128

// Sender 是 Model 的出站抽象。
//
// 这样 Model 只依赖一个窄接口而不是 *client.Client：
//   - 单元测试可以塞一个记录调用的假实现，不需要起 Hub；
//   - M5 换传输层（TCP / QUIC）时 Model 一行不用改。
type Sender interface {
	Send(ctx context.Context, body string) error
}

// Session 是 TUI 与 pkg/client 之间的适配层，负责连接的完整生命周期。
//
// 职责边界：
//   - 入站：Session.Pump 把 EventBus 上的事件推给 sink（通常是 Model.Publish）；
//   - 出站：Session.Send 实现 Sender，供 Model 的 submitMsg 调用；
//   - 生命周期：Dial 建连、Close 释放，均幂等。
//
// Model 不持有 Session，只持有 Sender 接口 —— 这样 TUI 状态机始终可单测。
type Session struct {
	cli    *client.Client
	sub    core.Subscription
	store  core.Store
	convID string

	done      chan struct{}
	closeOnce sync.Once
}

// DialOptions 是 Dial 的入参。Transport 必填，其余有兜底。
type DialOptions struct {
	// Transport 决定底层连接如何建立：生产用 pkg/transport/ws，
	// 测试用 pkg/transport/fake。这就是 ADR-002 的可替换点。
	Transport core.Transport
	// HubURL 形如 ws://127.0.0.1:9000 或 127.0.0.1:9000。
	HubURL string
	// User / Device 组成 ADR-008 的二层身份；Device 同时是 ReadCursor 主键。
	User   string
	Device string
	// ConvID 为空时回落到 DefaultConversationID。
	ConvID string
	// HistoryLimit 是首屏历史拉取条数，<=0 用 client 默认值。
	HistoryLimit int
}

// Dial 建立连接并完成握手（Hello + 可选历史补发）。
//
// Store 与 EventBus 在 Session 内部创建并由 Close 统一释放，调用方无需感知。
// 返回的 Session 必须在使用结束后 Close。
func Dial(ctx context.Context, opts DialOptions) (*Session, error) {
	if opts.Transport == nil {
		return nil, errors.New("tui: DialOptions.Transport is required")
	}
	convID := opts.ConvID
	if convID == "" {
		convID = DefaultConversationID
	}

	hello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        opts.Device,
		UserID:          opts.User,
	}

	conn, err := opts.Transport.Dial(ctx, opts.HubURL, hello)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opts.HubURL, err)
	}

	store := memory.New()
	bus := event.New()
	cli := client.New(hello, conn, store, bus)

	// Dial 成功但 Connect 失败时，conn 与 store 都得回收，否则 fd / 内存泄漏。
	if err := cli.Connect(ctx, client.ConnectOptions{
		RequestHistory: true,
		HistoryLimit:   opts.HistoryLimit,
	}); err != nil {
		_ = conn.Close()
		_ = store.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}

	return &Session{
		cli:    cli,
		sub:    cli.Subscribe(eventBuf),
		store:  store,
		convID: convID,
		done:   make(chan struct{}),
	}, nil
}

// Send 实现 Sender：把一行文本发往会话。
//
// ctx 由调用方（Model.sendCmd）控制超时；这里不再二次设 deadline。
// 消息发出后不等回执 —— Hub 会通过 FKDeliver 回送，届时由 Pump 反映到 UI。
func (s *Session) Send(ctx context.Context, body string) error {
	return s.cli.SendMessage(ctx, s.convID, body)
}

// ConversationID 返回本 Session 绑定的会话 ID。
func (s *Session) ConversationID() string { return s.convID }

// Events 暴露底层订阅通道，供需要自建循环的调用方使用。
// 常规路径应直接用 Pump。
func (s *Session) Events() <-chan core.Event { return s.sub.C() }

// Pump 把连接上的事件持续投递给 sink，直到 ctx 取消或连接断开。
//
// 退出条件是显式的两种，**不能用 `for range Events()`**：
// pkg/event 的 sub.Close() 只是把订阅者从 bus 上摘掉并标记 alive=false，
// 并不关闭 channel，range 会永久阻塞。
//
// 连接断开时先 drain 一次缓冲：client.Done()（readPump 退出）与最后几条
// Publish 之间存在竞态，直接 return 会丢掉已经进入 channel 的事件。
func (s *Session) Pump(ctx context.Context, sink func(core.Event)) {
	if sink == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-s.cli.Done():
			s.drain(sink)
			return
		case e := <-s.sub.C():
			sink(e)
		}
	}
}

// drain 非阻塞排空订阅缓冲里的残留事件。
func (s *Session) drain(sink func(core.Event)) {
	for {
		select {
		case e := <-s.sub.C():
			sink(e)
		default:
			return
		}
	}
}

// Done 在 Close 被调用后关闭。
func (s *Session) Done() <-chan struct{} { return s.done }

// Close 按 sub → client → store 的顺序释放资源，幂等。
//
// 顺序有讲究：先摘订阅（之后 Publish 不再投递），再关 client
// （内部会等 readPump 退出），最后关 store —— 反过来的话 readPump
// 可能还在往一个已关闭的 store 里写。
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		if e := s.sub.Close(); e != nil && err == nil {
			err = e
		}
		if e := s.cli.Close(); e != nil && err == nil {
			err = e
		}
		if e := s.store.Close(); e != nil && err == nil {
			err = e
		}
	})
	return err
}

// 编译期断言：Session 可作为 Model 的出站实现。
var _ Sender = (*Session)(nil)
