package hubstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	seq    *Sequencer
	reg    *Registry
	hist   *History
	convs  *Convs
	store  core.Store
	nodeID string

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
	// NodeID 是本 hub 在去中心化 mesh（ADR-014）中的节点标识。
	// 非空时，落库消息若未带源节点（v1 客户端不传 node_id），
	// 盖上本节点 ID——消息坐标 (NodeID, ServerSeq) 由此成立。
	NodeID string
}

// defaultMaxHistoryLimit 是单次补发的默认条数上限。
const defaultMaxHistoryLimit = 500

// NewRouter 构造一个 Router。cfg 为 nil 时全部走默认。
func NewRouter(ctx context.Context, cfg *RouterConfig) *Router {
	if cfg == nil {
		cfg = &RouterConfig{}
	}
	limit := cfg.MaxHistoryLimit
	if limit <= 0 {
		limit = defaultMaxHistoryLimit
	}
	r := &Router{
		seq:             NewSequencer(cfg.StartSeq),
		reg:             NewRegistry(),
		hist:            NewHistory(),
		convs:           NewConvs(),
		store:           cfg.Store,
		nodeID:          cfg.NodeID,
		maxHistoryLimit: limit,
	}
	// M12-A：hub 重启后从 store 恢复群与会话成员。
	r.convs.LoadFromStore(ctx, cfg.Store)
	return r
}

// Registry 暴露注册表，供调用方做 Presence 广播等跨连接操作。
func (r *Router) Registry() *Registry { return r.reg }

// History 暴露补发缓冲，供调用方做灾后恢复（读 MaxSeq 重置 Sequencer）。
func (r *Router) History() *History { return r.hist }

// Convs 暴露会话注册表，供调用方（如集成测试）直接观测群状态。
func (r *Router) Convs() *Convs { return r.convs }

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
		// 注销必须先于 Close：先摘注册表，后续 broadcastPresence 的快照里
		// 自然不含本连接，offline 帧不会回发给正在关闭的自己。
		id, _ := r.reg.Remove(peerID)
		_ = p.Close()
		routerLog.Info("peer detached", "peer", peerID)
		// M7.2：握过手的设备离场时广播 offline。用 WithoutCancel 派生 ctx：
		// 退出原因往往就是 ctx 取消/Recv 失败，但广播本身不应被连接生命周期
		// 一起取消（best-effort 通知其余在线者）。同设备仍有连接在册
		// （断线重连新旧共存）时 HasDevice 防抖跳过。
		if id.HelloOK && id.DeviceID != "" && !r.reg.HasDevice(id.DeviceID) {
			r.broadcastPresence(context.WithoutCancel(ctx), protocol.Presence{
				UserID: id.UserID, DeviceID: id.DeviceID, Online: false,
			})
		}
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
		return r.handleMessage(ctx, peerID, p, f)

	case protocol.FKHistoryReq:
		return r.handleHistoryReq(ctx, p, f)

	case protocol.FKAck:
		return r.handleAck(ctx, p, f)

	case protocol.FKRead:
		return r.handleRead(ctx, peerID, f)

	case protocol.FKConvCreate:
		return r.handleConvCreate(ctx, peerID, p, f)

	case protocol.FKConvInvite:
		return r.handleConvInvite(ctx, peerID, p, f)

	case protocol.FKConvLeave:
		return r.handleConvLeave(ctx, peerID, p, f)

	case protocol.FKSearchReq:
		return r.handleSearchReq(ctx, peerID, p, f)

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
	if err := r.announceOnline(ctx, p, hello); err != nil {
		return fatalf("announce online: %v", err)
	}
	return nil
}

// announceOnline 是握手成功后的在线状态广播（M7.2）：
//
//  1. 先给新连接单发当前在线名单（不含自己）——让它一上来就能渲染出
//     「现在谁在线」，而不是干等下一次有人上下线；
//  2. 再向全体（含自己）广播自己的上线——别人据此把该设备加进列表，
//     自己收到回显也无妨，客户端按 DeviceID upsert 天然幂等。
//
// 名单单发失败视为连接已坏（返回 error 由 handleHello 判 fatal 关连接）：
// 连握手后的第一帧都写不进去，这条连接没有继续服务的价值。
// 后续全员广播与消息广播同语义（best-effort），单条失败不断连。
// 设备身份为空的连接（异常握手）跳过广播，避免产生无法归属的 Presence。
func (r *Router) announceOnline(ctx context.Context, p Peer, hello protocol.Hello) error {
	if hello.DeviceID == "" {
		return nil
	}
	for _, pr := range r.reg.OnlinePresence(hello.DeviceID) {
		if err := r.sendPresence(ctx, p, pr); err != nil {
			return err
		}
	}
	// M8.1：补发已读游标快照，让新连接立刻能渲染「谁已读到哪」，
	// 不用干等下一次有人读消息。写失败与 presence 名单同语义（连接已坏）。
	if err := r.sendReadSnapshot(ctx, p, hello.DeviceID); err != nil {
		return err
	}
	// M12-A：下发会话列表快照（群），让新连接立刻能渲染会话侧栏。
	if err := r.sendConvSnapshot(ctx, p); err != nil {
		return err
	}
	r.broadcastPresence(ctx, protocol.Presence{
		UserID: hello.UserID, DeviceID: hello.DeviceID, Online: true,
	})
	return nil
}

// sendReadSnapshot 把 store 里各设备的已读游标逐个发给新握手的连接。
//
// 游标里没有 UserID（read_cursors 表只存 device_id）：在线设备从注册表
// 补，离线设备从设备表（GetDevice）补；都查不到就发空 UserID（UI 退化
// 成显示 device 名，不影响功能）。新连接自己的游标不发——自己读没读
// 自己知道。
func (r *Router) sendReadSnapshot(ctx context.Context, p Peer, selfDevice string) error {
	if r.store == nil {
		return nil
	}
	cursors, err := r.store.ListCursors(ctx, "")
	if err != nil {
		routerLog.Warn("list read cursors for snapshot failed", "err", err)
		return nil // 快照是锦上添花，查库失败不阻断握手
	}
	for _, rc := range cursors {
		if rc.DeviceID == "" || rc.DeviceID == selfDevice || rc.ServerSeq == 0 {
			continue
		}
		if rc.UserID == "" {
			if u, ok := r.reg.UserOfDevice(rc.DeviceID); ok {
				rc.UserID = u
			} else if d, err := r.store.GetDevice(ctx, rc.DeviceID); err == nil {
				rc.UserID = d.UserID
			}
		}
		payload, err := json.Marshal(rc)
		if err != nil {
			continue // 单条 marshal 失败跳过
		}
		if err := p.Send(ctx, protocol.Frame{Kind: protocol.FKRead, Payload: payload}); err != nil {
			return fmt.Errorf("send read snapshot: %w", err)
		}
	}
	return nil
}

// sendPresence 给单条连接发一个 Presence 帧。
func (r *Router) sendPresence(ctx context.Context, p Peer, pr protocol.Presence) error {
	payload, err := json.Marshal(pr)
	if err != nil {
		//nolint:nilerr // 序列化 Presence 不可能失败；失败了也无补救动作
		return nil
	}
	if err := p.Send(ctx, protocol.Frame{Kind: protocol.FKPresence, Payload: payload}); err != nil {
		return fmt.Errorf("send presence: %w", err)
	}
	return nil
}

// broadcastPresence 把一个 Presence 帧广播给所有已握手连接。
func (r *Router) broadcastPresence(ctx context.Context, pr protocol.Presence) {
	payload, err := json.Marshal(pr)
	if err != nil {
		//nolint:nilerr // 序列化 Presence 不可能失败
		return
	}
	r.broadcast(ctx, protocol.Frame{Kind: protocol.FKPresence, Payload: payload})
}

// handleMessage 是写路径：分配序号 → 落库 → 进缓冲 → 广播。
//
// 顺序很重要：
//  1. 先分配 seq 再落库 —— 保证库里的 seq 与广播出去的一致
//  2. 先落库再广播 —— 「先落库后投递」是 core.Store 的契约，
//     这样客户端收到 FKDeliver 后再来 History 一定能查到
func (r *Router) handleMessage(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	var m protocol.StoredMessage
	if err := json.Unmarshal(f.Payload, &m); err != nil {
		// 单帧解析失败不关连接：可能只是这一条消息格式有问题，
		// 断连会误伤同一连接上的其它正常流量。
		//nolint:nilerr // 故意丢弃：单条坏消息不足以判连接死刑，错误详情只进日志
		return nil
	}

	// M12-A：发送者身份以注册表为准（同 typing/read 信任模型）——
	// 客户端上报的 SenderUserID/DeviceID 一律覆盖，防伪造他人发言。
	if id, ok := r.reg.IdentityOf(peerID); ok {
		m.SenderUserID = id.UserID
		m.SenderDeviceID = id.DeviceID
	}

	// M12-A：权限校验——只有群成员能向群发消息；大厅（空 conv）全员放行。
	if !r.convs.IsLobby(m.ConversationID) && !r.convs.IsMember(m.ConversationID, m.SenderUserID) {
		routerLog.Warn("message rejected: not a member",
			"conv", m.ConversationID, "user", m.SenderUserID)
		r.sendError(ctx, p, protocol.ErrForbidden, "not a member of this conversation")
		return nil
	}

	m.ServerSeq = r.seq.Next()
	if m.CreatedAt == 0 {
		// Hub 时间为准：客户端时钟不可信（AGENTS.md 架构铁律 §3.4）
		m.CreatedAt = time.Now().UnixMilli()
	}

	routerLog.Info("message received",
		"seq", m.ServerSeq, "from", m.SenderUserID, "dev", m.SenderDeviceID, "conv", m.ConversationID)

	if r.nodeID != "" && m.NodeID == "" {
		m.NodeID = r.nodeID
	}
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

	// 广播给会话成员（含发送者自己，客户端按 ID 去重）；大厅 = 全员。
	payload, err := json.Marshal(m)
	if err != nil {
		//nolint:nilerr // 序列化 StoredMessage 不可能失败；失败了也无补救动作
		return nil
	}
	r.broadcastToConv(ctx, m.ConversationID, protocol.Frame{Kind: protocol.FKDeliver, Payload: payload})
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
	resp := r.hist.Query(convID, req.After, req.Before, limit)

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

// handleSearchReq 处理历史搜索请求（FKSearchReq，v1.1）。
//
// 权限：大厅（空 conv）全员可见；群搜索结果只保留发起者是成员的群——
// 先全量搜（store 层不感知会话权限），再按 Convs.IsMember 过滤，避免
// 非成员通过搜索窥探群消息。结果只回发起连接（per-device，同补发）。
func (r *Router) handleSearchReq(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.UserID == "" {
		return nil
	}
	var req protocol.SearchRequest
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad search request")
			return nil //nolint:nilerr // 错误已由 sendError 帧下发
		}
	}
	if strings.TrimSpace(req.Query) == "" {
		r.sendError(ctx, p, protocol.ErrInvalidFrame, "query required")
		return nil //nolint:nilerr // 错误已由 sendError 帧下发
	}
	if r.store == nil {
		r.sendError(ctx, p, protocol.ErrInternal, "search unavailable (no store)")
		return nil //nolint:nilerr // 错误已由 sendError 帧下发
	}
	hits, err := r.store.SearchMessages(ctx, req.Query, req.ConversationID, req.Limit)
	if err != nil {
		routerLog.Warn("search failed", "err", err)
		r.sendError(ctx, p, protocol.ErrInternal, "search failed")
		return nil //nolint:nilerr // 错误已由 sendError 帧下发
	}
	// 会话权限过滤：非成员群的消息剔除。
	if req.ConversationID == "" {
		filtered := hits[:0]
		for _, h := range hits {
			if r.convs.IsLobby(h.ConversationID) || r.convs.IsMember(h.ConversationID, id.UserID) {
				filtered = append(filtered, h)
			}
		}
		hits = filtered
	}
	payload, err := json.Marshal(protocol.SearchResponse{Hits: hits})
	if err != nil {
		//nolint:nilerr // 序列化 SearchResponse 不可能失败
		return nil
	}
	if err := p.Send(ctx, protocol.Frame{Kind: protocol.FKSearchResp, Payload: payload}); err != nil {
		return fatalf("send search resp: %v", err)
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

// handleRead 处理「已读」上报（M8.1）：
//
//  1. 身份以注册表为准（客户端只上报 conv+seq，无权声明身份）；
//  2. seq 钳制到当前已分配的最大序号——序号是 Hub 发的，客户端游标
//     不可能超过 Hub 已知范围（hub 重启后旧客户端的残留帧尤其要防）；
//  3. 落 per-device 游标（store 内 UPSERT+MAX 单调不回退）；
//  4. 把盖戳后的 ReadCursor 广播给除发送者外的所有连接——同用户的
//     其它设备也收（多设备已读状态同步）。
//
// 未握手连接、空会话号静默丢弃；落库失败只 Warn 不阻断广播（best-effort）。
func (r *Router) handleRead(ctx context.Context, peerID uint64, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.DeviceID == "" {
		return nil
	}
	var rd protocol.Read
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &rd); err != nil {
			//nolint:nilerr // 游标是幂等状态，坏帧忽略即可，下次 Read 会再写
			return nil
		}
	}
	// M12-A：空 conv = 大厅，合法会话；不再丢弃。
	seq := rd.ServerSeq
	// 序号是 Hub 发的，游标不应超过已分配的最大 seq（防 hub 重启后
	// 旧客户端的残留帧把已读状态标到未来）。max==0（本进程还没发过
	// 任何消息）时不钳制——此时没有消息可被误标。
	if last := r.seq.Last(); last > 0 && seq > last {
		seq = last
	}
	if r.store != nil {
		if err := r.store.SetCursor(ctx, id.DeviceID, rd.ConversationID, seq); err != nil {
			routerLog.Warn("persist read cursor failed", "device", id.DeviceID, "conv", rd.ConversationID, "seq", seq, "err", err)
		}
	}
	rc := protocol.ReadCursor{
		UserID:         id.UserID,
		DeviceID:       id.DeviceID,
		ConversationID: rd.ConversationID,
		ServerSeq:      seq,
	}
	payload, err := json.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshal read cursor: %w", err)
	}
	out := protocol.Frame{Kind: protocol.FKRead, Payload: payload}
	// M12-A：已读回执只发给同会话成员（大厅 = 全员，排除发送者）。
	peers := r.convPeers(rd.ConversationID, peerID)
	routerLog.Debug("read broadcast", "peer", peerID, "device", id.DeviceID, "conv", rd.ConversationID, "seq", seq, "recipients", len(peers))
	_ = SendToPeers(ctx, peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, out)
	})
	return nil
}

// handleTyping 转发「正在输入」给除发送者外的所有已握手连接（M7.3）。
//
// 不持久化：瞬时提示，重启后没有意义（与 Presence 同理）。
// 客户端上发的负载被忽略——身份以注册表为准（客户端无权声明他人身份），
// Hub 盖上发送者的 UserID/DeviceID 后再广播，接收方据此渲染「谁在输入」。
// 未握手连接的 typing 静默丢弃；单次提示丢失无所谓，best-effort 发送。
func (r *Router) handleTyping(ctx context.Context, peerID uint64, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.DeviceID == "" {
		return nil
	}
	// M12-A：客户端上发时声明所在会话（空 = 大厅）；身份仍以注册表盖戳。
	convID := LobbyConvID
	if len(f.Payload) > 0 {
		var req protocol.Typing
		if err := json.Unmarshal(f.Payload, &req); err == nil {
			convID = req.ConversationID
		}
	}
	payload, err := json.Marshal(protocol.Typing{
		UserID: id.UserID, DeviceID: id.DeviceID, ConversationID: convID,
	})
	if err != nil {
		// Typing 是固定简单结构，marshal 实践中不会失败；真失败了把
		// 错误抛给上层日志，而不是静默吞掉（nilerr）。
		return fmt.Errorf("marshal typing: %w", err)
	}
	out := protocol.Frame{Kind: protocol.FKTyping, Payload: payload}
	// M12-A：typing 只发给同会话成员（排除发送者本人）。
	peers := r.convPeers(convID, peerID)
	routerLog.Debug("typing broadcast", "peer", peerID, "device", id.DeviceID, "conv", convID, "recipients", len(peers))
	_ = SendToPeers(ctx, peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, out)
	})
	return nil
}

// ---- M12-A 会话管理 ----

// sendConvSnapshot 给单条连接下发全部群快照（FKConvList）。握手后调用，
// 与 presence/read 名单同语义：新连接一上来就能渲染会话列表。
// 大厅不在此列——客户端把空会话 ID 隐式渲染为「大厅」。
func (r *Router) sendConvSnapshot(ctx context.Context, p Peer) error {
	snaps := r.convs.ListSnapshots()
	payload, err := json.Marshal(snaps)
	if err != nil {
		return fmt.Errorf("marshal conv snapshot: %w", err)
	}
	if err := p.Send(ctx, protocol.Frame{Kind: protocol.FKConvList, Payload: payload}); err != nil {
		return fmt.Errorf("send conv snapshot: %w", err)
	}
	return nil
}

// broadcastConvEvent 把一个会话变更事件广播给指定用户集合（M12-A）。
func (r *Router) broadcastConvEvent(ctx context.Context, ev protocol.ConversationEvent, userIDs []string, exclude uint64) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	out := protocol.Frame{Kind: protocol.FKConvEvent, Payload: payload}
	peers := r.reg.PeersForUsers(userIDs, exclude)
	routerLog.Debug("conv event broadcast", "conv", ev.Conversation.ID, "event", ev.Event, "recipients", len(peers))
	_ = SendToPeers(ctx, peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, out)
	})
}

// handleConvCreate 处理建群请求（FKConvCreate）。
//
// 初始成员 = 创建者 + 请求列表（Create 内去重）。创建成功后把
// "created" 事件广播给全部初始成员（含创建者，客户端按 ID 去重）。
func (r *Router) handleConvCreate(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.UserID == "" {
		return nil
	}
	var req protocol.ConversationRequest
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad conversation create")
			return nil //nolint:nilerr // 错误已由 sendError 帧下发，函数级保持 nil
		}
	}
	if req.Title == "" {
		r.sendError(ctx, p, protocol.ErrInvalidFrame, "title required")
		return nil
	}
	memberIDs := append([]string{id.UserID}, req.MemberIDs...)
	conv, err := r.convs.Create(ctx, req.Title, memberIDs, r.store)
	if err != nil {
		r.sendError(ctx, p, protocol.ErrInternal, "create conversation failed")
		return nil //nolint:nilerr // 错误已由 sendError 帧下发，函数级保持 nil
	}
	ev := protocol.ConversationEvent{
		Conversation: conv, Event: "created", ByUserID: id.UserID,
		Members: r.convs.Members(conv.ID),
	}
	r.broadcastConvEvent(ctx, ev, r.convs.Members(conv.ID), 0)
	return nil
}

// handleConvInvite 处理邀请成员请求（FKConvInvite）。
//
// 权限：仅群成员可邀请（大厅无需邀请）。成功后广播 "joined" 给群内
// 成员（含被邀请者），ByUserID 为邀请者。
func (r *Router) handleConvInvite(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.UserID == "" {
		return nil
	}
	var ref protocol.ConversationRef
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &ref); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad conversation invite")
			return nil //nolint:nilerr // 错误已由 sendError 帧下发，函数级保持 nil
		}
	}
	if ref.ConversationID == "" || len(ref.UserIDs) == 0 {
		r.sendError(ctx, p, protocol.ErrInvalidFrame, "conversation id and users required")
		return nil //nolint:nilerr // 错误已由 sendError 帧下发，函数级保持 nil
	}
	if !r.convs.IsMember(ref.ConversationID, id.UserID) {
		r.sendError(ctx, p, protocol.ErrForbidden, "not a member of this conversation")
		return nil
	}
	added, ok := r.convs.Invite(ctx, ref.ConversationID, ref.UserIDs, r.store)
	if !ok {
		r.sendError(ctx, p, protocol.ErrInvalidFrame, "conversation not found")
		return nil
	}
	if len(added) == 0 {
		return nil // 全已在群里，无变更
	}
	cv, _ := r.convs.Get(ref.ConversationID)
	ev := protocol.ConversationEvent{
		Conversation: cv, Event: "joined", ByUserID: id.UserID,
		Members: r.convs.Members(ref.ConversationID),
	}
	r.broadcastConvEvent(ctx, ev, r.convs.Members(ref.ConversationID), 0)
	return nil
}

// handleConvLeave 处理退群请求（FKConvLeave）。
//
// 大厅不可退出；群内成员退群后广播 "left" 给剩余成员。
func (r *Router) handleConvLeave(ctx context.Context, peerID uint64, p Peer, f protocol.Frame) error {
	id, ok := r.reg.IdentityOf(peerID)
	if !ok || id.UserID == "" {
		return nil
	}
	var ref protocol.ConversationRef
	if len(f.Payload) > 0 {
		if err := json.Unmarshal(f.Payload, &ref); err != nil {
			r.sendError(ctx, p, protocol.ErrInvalidFrame, "bad conversation leave")
			return nil //nolint:nilerr // 错误已由 sendError 帧下发，函数级保持 nil
		}
	}
	if ref.ConversationID == "" {
		r.sendError(ctx, p, protocol.ErrInvalidFrame, "conversation id required")
		return nil
	}
	if r.convs.IsLobby(ref.ConversationID) {
		r.sendError(ctx, p, protocol.ErrForbidden, "cannot leave lobby")
		return nil
	}
	if !r.convs.Leave(ctx, ref.ConversationID, id.UserID, r.store) {
		return nil // 不在群里或会话不存在，静默忽略
	}
	cv, _ := r.convs.Get(ref.ConversationID)
	ev := protocol.ConversationEvent{Conversation: cv, Event: "left", ByUserID: id.UserID}
	r.broadcastConvEvent(ctx, ev, r.convs.Members(ref.ConversationID), 0)
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

// broadcastToConv 把一帧广播给某会话的全部成员（M12-A）。
//
//   - 大厅（空 conv）：发给所有已握手连接（含发送者，客户端按 ID 去重）；
//   - 群：发给全部成员的在线设备（成员用户之间天然互斥，无需去重）。
func (r *Router) broadcastToConv(ctx context.Context, convID string, f protocol.Frame) {
	peers := r.convPeers(convID, 0)
	routerLog.Debug("conv broadcast", "kind", f.Kind, "conv", convID, "peer_count", len(peers))
	_ = SendToPeers(ctx, peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, f)
	})
}

// convPeers 返回某会话的投递目标连接集合；excludePeerID 非空时跳过
// 该连接（消息回显、typing/read 排除发送者本人）。
func (r *Router) convPeers(convID string, excludePeerID uint64) []Peer {
	if r.convs.IsLobby(convID) {
		return r.reg.OthersPeers(excludePeerID)
	}
	return r.reg.PeersForUsers(r.convs.Members(convID), excludePeerID)
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
