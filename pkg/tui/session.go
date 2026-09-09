package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// DefaultConversationID 是默认会话 ID（M12-A 起统一为空串 = 大厅）。
const DefaultConversationID = ""

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

// HistoryFetcher 是 Sender 的可选能力（M7.1）：用户上翻到顶时向 hub
// 拉取更早的历史。Session 实现该接口；测试 fake 不实现时 Model 静默
// 禁用分页（类型断言失败即 no-op）。
type HistoryFetcher interface {
	FetchHistory(ctx context.Context, before uint64, limit int) ([]protocol.StoredMessage, bool, error)
}

// Typer 是 Sender 的可选能力（M7.3）：用户输入时向 hub 上发「正在输入」。
// Session 实现该接口；测试 fake 不实现时 Model 静默禁用（类型断言失败 no-op）。
type Typer interface {
	SendTyping(ctx context.Context) error
}

// ReplySender 是 Sender 的可选能力（v1.1）：发送引用回复。
// Session 实现该接口；测试 fake 不实现时 Model 静默禁用 /reply。
type ReplySender interface {
	SendReply(ctx context.Context, body string, ref *protocol.ReplyRef) error
}

// Searcher 是 Sender 的可选能力（v1.1）：按关键词搜索历史消息。
// Session 实现该接口；测试 fake 不实现时 Model 静默禁用 /search。
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]protocol.StoredMessage, error)
}

// Reader 是 Sender 的可选能力（M8.1）：用户已读时向 hub 上发已读回执。
// 会话绑定 ConversationID，接口只收 ServerSeq；Session 实现该接口，
// 测试 fake 不实现时 Model 静默禁用（类型断言失败 no-op）。
type Reader interface {
	SendRead(ctx context.Context, serverSeq uint64) error
}

// ConversationManager 是 Sender 的可选能力（M12-A）：会话列表/切换/
// 建群/邀请/退群。Session 实现该接口；测试 fake 不实现时 Model 静默
// 禁用对应命令（类型断言失败 no-op + 提示）。
type ConversationManager interface {
	// SetConversation 切换当前会话（空串 = 大厅）。会话 ID 由 hub 生成，
	// 之后本 Session 的 Send/SendTyping/SendRead/FetchHistory 都发往它。
	SetConversation(convID string)
	// Conversations 返回本地会话快照（大厅合成排第一，其余按 ID 升序）。
	Conversations() []protocol.ConversationSnapshot
	// CreateConversation 建群：hub 生成 ID 后经 FKConvEvent 广播回来，
	// 本地快照随之更新（不等同步返回）。
	CreateConversation(ctx context.Context, title string, memberIDs []string) (protocol.Conversation, error)
	// InviteToConversation 邀请用户进群（须是当前成员）。
	InviteToConversation(ctx context.Context, convID string, userIDs []string) error
	// LeaveConversation 退群（群主退群后群保留给剩余成员）。
	LeaveConversation(ctx context.Context, convID string) error
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

	// M9：文件传输的数据面挂在 hub 的 HTTP 端口（与 WS 同端口），
	// 从 ws URL 推导 http 基址注入 client；不额外要求第二个地址。
	cli.SetFileBase(client.HTTPBaseFromWS(opts.HubURL))

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

// SendReply 实现 ReplySender（v1.1）：把一条引用回复发给当前会话。
func (s *Session) SendReply(ctx context.Context, body string, ref *protocol.ReplyRef) error {
	return s.cli.SendMessage(ctx, s.convID, body, ref)
}

// Search 实现 Searcher（v1.1）：按关键词搜索（含当前会话限定，
// convID 为空 = 搜索全部会话）。返回降序命中列表。
func (s *Session) Search(ctx context.Context, query string, limit int) ([]protocol.StoredMessage, error) {
	resp, err := s.cli.Search(ctx, query, s.convID, limit)
	if err != nil {
		return nil, err
	}
	return resp.Hits, nil
}

// Send 实现 Sender：把一行文本发往会话。
//
// ctx 由调用方（Model.sendCmd）控制超时；这里不再二次设 deadline。
// 消息发出后不等回执 —— Hub 会通过 FKDeliver 回送，届时由 Pump 反映到 UI。
func (s *Session) Send(ctx context.Context, body string) error {
	return s.cli.SendMessage(ctx, s.convID, body)
}

// FetchHistory 向 hub 请求 before 之前的一页历史（M7.1 上翻分页）。
//
// 返回消息按 server_seq 升序；hasMore=false 表示更早没有了。
// 响应消息同时由 client 落本地 Store（与 web 端 /history 同路径）。
func (s *Session) FetchHistory(ctx context.Context, before uint64, limit int) ([]protocol.StoredMessage, bool, error) {
	resp, err := s.cli.FetchHistory(ctx, s.convID, 0, before, limit)
	if err != nil {
		return nil, false, err
	}
	return resp.Messages, resp.HasMore, nil
}

// SendTyping 实现 Typer：上发「正在输入」提示（M7.3 + M12-A）。
// 会话 ID 由 Session 绑定（typing 只广播给同会话成员）；身份由 hub
// 盖戳；节流由 Model 负责。
func (s *Session) SendTyping(ctx context.Context) error {
	return s.cli.SendTyping(ctx, s.convID)
}

// SendRead 实现 Reader：上发「已读到 seq」回执（M8.1）。
// 会话 ID 由 Session 绑定，调用方无需感知。
func (s *Session) SendRead(ctx context.Context, serverSeq uint64) error {
	return s.cli.SendRead(ctx, s.convID, serverSeq)
}

// ConversationID 返回本 Session 绑定的会话 ID。
func (s *Session) ConversationID() string { return s.convID }

// SetConversation 切换当前会话（M12-A）。空串 = 大厅。
func (s *Session) SetConversation(convID string) { s.convID = convID }

// Conversations 返回本地会话快照（client 层已合成大厅、按 ID 升序）。
func (s *Session) Conversations() []protocol.ConversationSnapshot {
	return s.cli.Conversations()
}

// CreateConversation 建群（M12-A）。返回的 Conversation 只有本地请求的
// 字段（ID 为空——hub 生成后经 FKConvEvent 广播，UI 用 /rooms 刷新）。
func (s *Session) CreateConversation(ctx context.Context, title string, memberIDs []string) (protocol.Conversation, error) {
	return s.cli.CreateConversation(ctx, title, memberIDs)
}

// InviteToConversation 邀请用户进群（M12-A）。
func (s *Session) InviteToConversation(ctx context.Context, convID string, userIDs []string) error {
	return s.cli.InviteToConversation(ctx, convID, userIDs)
}

// LeaveConversation 退群（M12-A）。
func (s *Session) LeaveConversation(ctx context.Context, convID string) error {
	return s.cli.LeaveConversation(ctx, convID)
}

// FileSender / FileReceiver 是 Model 可选注入的文件收发能力（M9）。
// 与 Sender/Typer/Reader 同一模式：Session 实现，测试可注入 fake。
type FileSender interface {
	// SendFile 上传本地文件并发送一条带附件引用的消息。
	SendFile(ctx context.Context, path string) error
}

// FileReceiver 接收端能力：把消息附件下载到本地。
type FileReceiver interface {
	// SaveFile 把消息附件下载到本地（lanchat-files/ 目录），返回保存路径。
	SaveFile(ctx context.Context, m protocol.StoredMessage) (string, error)
}

// SendFile 实现 FileSender：上传 + 发消息（hub 回环后事件流自会
// 出现该文件消息，Model 据此渲染附件卡片）。
func (s *Session) SendFile(ctx context.Context, path string) error {
	return s.cli.UploadFile(ctx, s.convID, path, "")
}

// SaveFile 实现 FileReceiver：把消息附件保存到本地 lanchat-files/<name>。
//
// 文件名来自 hub 的 FileRef.Name（hubfile 已 Base+255 截断清洗），
// 这里再用 filepath.Base 兜一层，绝不入目录路径。
func (s *Session) SaveFile(ctx context.Context, m protocol.StoredMessage) (string, error) {
	if m.File == nil {
		return "", errors.New("tui: save file: message has no file ref")
	}
	dest := filepath.Join("lanchat-files", filepath.Base(m.File.Name))
	if err := s.cli.DownloadFile(ctx, m.File.FileID, dest); err != nil {
		return "", err
	}
	return dest, nil
}

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
var (
	_ Sender         = (*Session)(nil)
	_ HistoryFetcher = (*Session)(nil)
	_ Typer          = (*Session)(nil)
	_ Reader         = (*Session)(nil)
)
