package hubstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// routerLog 是 Router 协议状态机的 logger。
var routerLog = logging.New("hubstate.router")

// Router 是 Hub 的核心状态机：把进来的 Frame 翻译成状态变更与投递。
//
// 它**完全不知道网络是怎么来的**——只通过 Peer 接口收发帧。
// 因此同一个 Router 同时服务于：
//   - fake Hub（内存 channel，M1 测试与 M2 回归）
//   - 真 Hub（WebSocket 服务端，M2 起）
//
// 这种「一份逻辑、两种传输」正是 ADR-002 想要的东西：
// M5 引入第二种 Transport 时，本文件应当零修改。
//
// 线程模型：
//   - 每条连接一条 goroutine（ServePeer 由调用方启动）
//   - Router 自身的字段全部并发安全（Sequencer 是 atomic，Registry/History 自带锁）
//   - Store 由调用方注入，其实现必须是并发安全的（memory / sqlite 都满足）
type Router struct {
	seq   *Sequencer
	reg   *Registry
	hist  *History
	store core.Store

	// maxHistoryLimit 是单次补发返回的最大条数上限。
	// 客户端请求的 limit 超过它时按它截断，防止一个请求把整个历史拖出来打爆内存。
	maxHistoryLimit int
}

// RouterConfig 是构造 Router 的可选项。零值字段走默认。
type RouterConfig struct {
	// Store 是落库目标。为 nil 时消息只进 History 不落盘（纯内存 Hub，测试用）。
	Store core.Store

	// StartSeq 是 ServerSeq 起始值。Hub 重启时应设为已落库的最大 seq，
	// 否则新消息序号会与历史撞车（详见 Sequencer 的注释）。
	StartSeq uint64

	// MaxHistoryLimit 是单次补发的条数上限，<=0 时取 defaultMaxHistoryLimit。
	MaxHistoryLimit int
}

// defaultMaxHistoryLimit 是单次补发的默认条数上限。
const defaultMaxHistoryLimit = 500

// NewRouter 构造一个 Router。cfg 为 nil 时全部走默认。
func NewRouter(cfg *RouterConfig) *Router {
	if cfg == nil {
		cfg = &RouterConfig{}
	}
	limit := cfg.MaxHistoryLimit
	if limit <= 0 {
		limit = defaultMaxHistoryLimit
	}
	return &Router{
		seq:             NewSequencer(cfg.StartSeq),
		reg:             NewRegistry(),
		hist:            NewHistory(),
		store:           cfg.Store,
		maxHistoryLimit: limit,
	}
}

// Registry 暴露注册表，供调用方做 Presence 广播等跨连接操作。
func (r *Router) Registry() *Registry { return r.reg }

// History 暴露补发缓冲，供调用方做灾后恢复（读 MaxSeq 重置 Sequencer）。
func (r *Router) History() *History { return r.hist }

// Sequencer 暴露序号分配器，便于测试断言。
func (r *Router) Sequencer() *Sequencer { return r.seq }

// ---- 连接生命周期 ----

// Attach 登记一条连接并在后台接管它的读循环，**同步返回**。
//
// 与 ServePeer 的区别只是阻塞与否：
//   - Attach 同步返回，适合 accept 循环（accept 完立刻接受下一条）
//   - ServePeer 阻塞到连接断开，适合调用方想自己管理 goroutine 的场景
//
// 返回 Hub 分配的 peerID，调用方可用于后续追踪（无需时忽略即可）。
//
// 之所以要「同步登记」：调用方常需要在返回后立刻观测连接数（如断言、
// 限流判断）。若登记发生在 goroutine 里，这里就会有一场数据竞争。
func (r *Router) Attach(ctx context.Context, p Peer) uint64 {
	peerID := r.reg.Add(p, p.DeviceID(), "")
	routerLog.Info("peer attached", "peer", peerID, "device", p.DeviceID())
	go r.serveLoop(ctx, peerID, p)
	return peerID
}

// ServePeer 接管一条连接，阻塞到它断开为止。
//
// 这是每条连接的读循环，通常由 Transport 在 accept 后 `go ServePeer(...)` 启动。
// 返回时连接已注销——调用方不需要再清理注册表。
//
// 退出条件（任一）：
//   - Recv 返回错误（对端关闭 / 传输层出错 / ctx 取消）
//   - HandleFrame 返回 error（协议违规，需要关连接）
func (r *Router) ServePeer(ctx context.Context, p Peer) {
	peerID := r.reg.Add(p, p.DeviceID(), "")
	r.serveLoop(ctx, peerID, p)
}

// serveLoop 是 Attach 与 ServePeer 共用的读循环主体。
func (r *Router) serveLoop(ctx context.Context, peerID uint64, p Peer) {
	defer func() {
		r.reg.Remove(peerID)
		_ = p.Close()
		routerLog.Info("peer detached", "peer", peerID)
	}()

	for {
		f, err := p.Recv(ctx)
		if err != nil {
			// 连接关了就是关了，没有可恢复的动作：
			// 重连由客户端发起，Hub 不保留"半死"的连接状态。
			return
		}
		if err := r.HandleFrame(ctx, peerID, p, f); err != nil {
			// 协议违规：能通知就通知一声再走
			if errors.Is(err, errFatal) {
				routerLog.Warn("fatal frame, closing peer", "peer", peerID, "kind", f.Kind, "err", err)
				r.sendError(ctx, p, protocol.ErrInvalidFrame, err.Error())
			}
			return
		}
	}
}

// errFatal 标记「应当告知对端并关闭连接」的错误。
// 用它而不是直接返回具体错误，是为了让 HandleFrame 的返回值语义单一：
// 要么 nil（继续），要么致命（关连接）。
var errFatal = errors.New("hubstate: fatal protocol error")

// fatalf 构造一个致命错误。
func fatalf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errFatal, fmt.Sprintf(format, args...))
}

// ---- 帧路由 ----

// HandleFrame 处理单帧，返回非 nil 表示连接应当关闭。
//
// 路由表（与 fake Hub M1 的行为严格对齐，除了补发改成走 History）：
//
//	FKHello      —— 校验协议版本，登记身份；不匹配 → FKError + 关连接
//	FKMessage    —— 分配 ServerSeq → 落库 → 进 History → 广播 FKDeliver
//	FKHistoryReq —— 查 History → 回 FKHistoryResp（只给请求的这一条连接）
//	FKAck        —— 写设备级游标（conv="*" 表示全局已收到位点）
//	FKRead       —— 写 per-device 会话游标（ADR-008）
//	FKPing       —— 回 FKPong
//	FKTyping     —— 广播给同会话其它设备（不持久化）
//	其它         —— 静默丢弃
func (r *Router) HandleFrame(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	routerLog.Debug("frame received", "peer", peerID, "kind", f.Kind, "len", len(f.Payload))
	switch f.Kind {

	case protocol.FKHello:
		return r.handleHello(ctx, peerID, p, f)

	case protocol.FKMessage:
		return r.handleMessage(ctx, f)

	case protocol.FKHistoryReq:
		return r.handleHistoryReq(ctx, p, f)

	case protocol.FKAck:
		return r.handleAck(ctx, p, f)

	case protocol.FKRead:
		return r.handleRead(ctx, p, f)

	case protocol.FKPing:
		// 心跳不携带状态，直接回。失败说明连接已死，交给读循环收尾。
		if err := p.Send(ctx, protocol.Frame{Kind: protocol.FKPong}); err != nil {
			return fatalf("send pong: %v", err)
		}
		return nil

	case protocol.FKTyping:
		return r.handleTyping(ctx, peerID, f)

	default:
		// 未知 kind 静默丢弃：新版客户端对旧 Hub 发的扩展帧不该导致断连。
		return nil
	}
}

// handleHello 校验协议版本并登记身份。
//
// 版本不匹配是**唯一会让 Hub 主动拒绝连接**的情况：
// 协议演进时宁可让旧客户端连不上，也不能让它按错的语义解析消息。
func (r *Router) handleHello(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	var hello protocol.Hello
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &hello); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad hello payload")
			return fatalf("unmarshal hello: %v", err)
		}
		if hello.ProtocolVersion != protocol.ProtocolVersion {
			routerLog.Warn("protocol version mismatch",
				"peer", peerID, "got", hello.ProtocolVersion, "want", protocol.ProtocolVersion)
			r.sendError(ctx, p, protocol.ErrProtocolMismatch,
				"unsupported protocol version")
			return fatalf("protocol version %d != %d",
				hello.ProtocolVersion, protocol.ProtocolVersion)
		}
	}
	// 握手通过：之后这条连接才参与投递
	r.reg.MarkHello(peerID, hello.DeviceID, hello.UserID)
	routerLog.Info("peer handshake ok", "peer", peerID, "user", hello.UserID, "device", hello.DeviceID)
	return nil
}

// handleMessage 是写路径：分配序号 → 落库 → 进缓冲 → 广播。
//
// 顺序很重要：
//  1. 先分配 seq 再落库 —— 保证库里的 seq 与广播出去的一致
//  2. 先落库再广播 —— 「先落库后投递」是 core.Store 的契约，
//     这样客户端收到 FKDeliver 后再来 History 一定能查到
func (r *Router) handleMessage(ctx context.Context, f protocol.Frame) error {
	var m protocol.StoredMessage
	if err := json.Unmarshal(f.Payload, &m); err != nil {
		// 单帧解析失败不关连接：可能只是这一条消息格式有问题，
		// 断连会误伤同一连接上的其它正常流量。
		//nolint:nilerr // 故意丢弃：单条坏消息不足以判连接死刑，错误详情只进日志
		return nil
	}

	m.ServerSeq = r.seq.Next()
	if m.CreatedAt == 0 {
		// Hub 时间为准：客户端时钟不可信（AGENTS.md 架构铁律 §3.4）
		m.CreatedAt = time.Now().UnixMilli()
	}

	routerLog.Info("message received",
		"seq", m.ServerSeq, "from", m.SenderUserID, "dev", m.SenderDeviceID, "conv", m.ConversationID)

	if r.store != nil {
		if err := r.store.AppendMessage(ctx, m); err != nil {
			routerLog.Error("store append failed", "seq", m.ServerSeq, "err", err)
			// 落库失败也不关连接，但**不广播**——
			// 宁可让客户端补发时拉到，也不能广播一条没存住的消息（重启就消失）
			//nolint:nilerr // 故意丢弃：断连会让整条会话的后续消息全丢，代价更大
			return nil
		}
	}
	r.hist.Append(m)

	// 广播给所有在线连接（含发送者自己，客户端按 ID 去重）
	payload, err := json.Marshal(m)
	if err != nil {
		//nolint:nilerr // 序列化 StoredMessage 不可能失败；失败了也无补救动作
		return nil
	}
	r.broadcast(ctx, protocol.Frame{Kind: protocol.FKDeliver, Payload: payload})
	return nil
}

// handleHistoryReq 处理补发请求，只回给发起的那一条连接。
//
// 注意这里**不用** broadcast：补发是 per-device 的。
// 同用户的另一台设备有自己的游标，不需要（也不应该）收到这次的补发结果。
func (r *Router) handleHistoryReq(ctx context.Context, p Peer, f protocol.Frame) error {
	var req protocol.HistoryRequest
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad history req")
			//nolint:nilerr // 已回 FKError；补发请求失败不影响连接存续
			return nil
		}
	}

	limit := req.Limit
	if limit <= 0 || limit > r.maxHistoryLimit {
		limit = r.maxHistoryLimit
	}

	// len==1 走单会话快速路径；0 或 >1 都按跨会话处理
	convID := ""
	if len(req.ConversationIDs) == 1 {
		convID = req.ConversationIDs[0]
	}
	resp := r.hist.Query(convID, req.After, limit)

	payload, err := json.Marshal(resp)
	if err != nil {
		//nolint:nilerr // 序列化 HistoryResponse 不可能失败
		return nil
	}
	if err := p.Send(ctx, protocol.Frame{
		Kind:    protocol.FKHistoryResp,
		Payload: payload,
	}); err != nil {
		return fatalf("send history resp: %v", err)
	}
	return nil
}

// handleAck 处理客户端声明的「我已收到 seq <= Ack 的所有消息」。
//
// 它以 conv="*" 写入一个全局游标：表示这台设备跨所有会话的整体进度。
// 与 FKRead 的 per-conversation 游标是两套东西，互不覆盖。
func (r *Router) handleAck(ctx context.Context, p Peer, f protocol.Frame) error {
	if r.store == nil || f.Ack == 0 {
		return nil
	}
	deviceID := r.deviceIDOf(p)
	if deviceID == "" {
		return nil
	}
	// error 忽略：游标写失败不影响消息投递，下次 Ack/Read 会再写一次
	_ = r.store.SetCursor(ctx, deviceID, "*", f.Ack)
	return nil
}

// handleRead 写 per-device 会话游标（ADR-008 的核心）。
func (r *Router) handleRead(ctx context.Context, p Peer, f protocol.Frame) error {
	if r.store == nil {
		return nil
	}
	var rd protocol.Read
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &rd); err != nil {
			//nolint:nilerr // 游标是幂等状态，坏帧忽略即可，下次 Read 会再写
			return nil
		}
	}
	if rd.ConversationID == "" {
		return nil
	}
	deviceID := r.deviceIDOf(p)
	if deviceID == "" {
		return nil
	}
	_ = r.store.SetCursor(ctx, deviceID, rd.ConversationID, rd.ServerSeq)
	return nil
}

// handleTyping 转发「正在输入」给同会话的其它连接。
//
// 不持久化：这是瞬时提示，重启后没有意义（与 Presence 同理）。
// 原始帧直接透传，Hub 不解析其内容——它只需要知道要发给谁。
func (r *Router) handleTyping(ctx context.Context, peerID uint64, f protocol.Frame) error {
	// 排除发送者自己：没人需要看到"我正在输入"的回显
	r.reg.mu.RLock()
	type item struct {
		id   uint64
		peer Peer
	}
	peers := make([]item, 0, len(r.reg.conns))
	for id, e := range r.reg.conns {
		if e.helloOK && id != peerID {
			peers = append(peers, item{id: id, peer: e.peer})
		}
	}
	r.reg.mu.RUnlock()

	var wg sync.WaitGroup
	for _, it := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			_ = p.Send(ctx, f) // 单次提示丢失无所谓
		}(it.peer)
	}
	wg.Wait()
	return nil
}

// ---- 辅助 ----

// broadcast 把一帧发给所有已握手的连接。单条失败不影响其它。
func (r *Router) broadcast(ctx context.Context, f protocol.Frame) {
	peers := r.reg.AllPeers()
	routerLog.Debug("broadcast", "kind", f.Kind, "peer_count", len(peers))
	_ = SendToPeers(ctx, peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, f)
	})
}

// deviceIDOf 取连接的设备 ID，优先用注册表里握手后的权威值。
//
// 注册表里的值优先，因为 FKHello 里声明的才是真实身份；
// Peer.DeviceID() 可能是 transport 层预填的临时值。
func (r *Router) deviceIDOf(p Peer) string {
	r.reg.mu.RLock()
	defer r.reg.mu.RUnlock()
	for _, e := range r.reg.conns {
		if e.peer == p {
			return e.deviceID
		}
	}
	return p.DeviceID()
}

// sendError 给某条连接发一个 FKError 帧。发送失败无处上报，只能忽略。
func (r *Router) sendError(ctx context.Context, p Peer, code protocol.ErrorCode, msg string) {
	payload, err := json.Marshal(protocol.ErrorPayload{Code: code, Message: msg})
	if err != nil {
		return
	}
	_ = p.Send(ctx, protocol.Frame{Kind: protocol.FKError, Payload: payload})
}

// Close 关停 Router：关闭并注销所有连接。
func (r *Router) Close() {
	r.reg.CloseAll()
}
