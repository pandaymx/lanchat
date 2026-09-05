// Package fake 提供 core.Transport 的同进程内存实现。
//
// 主要用途：
//  1. M1 单元测试：FakeTransport 与 MemoryStore 一起跑出"发→收→存→补发"场景，无需任何端口
//  2. 调试：在 IDE 里直接 step 单进程代码，不依赖网络栈
//
// 关键设计：
//   - 两个 conn 之间通过 *Hub 路由（Hub 是 fake 包内的"零号 hub"），不直接 1-1 wire
//   - Hub 持有一个 store，给 FKMessage 分配 ServerSeq、持久化、然后 FKDeliver 广播给所有 conn
//   - 客户端断开后，Hub 仍保存消息；下次带正确 Hello.ResumeFrom 重连时，Hub 主动 FKHistoryResp 补发
//
// 这就是 M2 真 Hub 的全部语义 + 网络层的简化版。M2 实装时把 conn 实现换成 WebSocket，
// Hub 本体可基本不动（仅 Listen/Dial 路径变化）。
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// mustJSON 序列化失败时返回 nil。Hub 路由路径不靠序列化结果做正确性判定。
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// addrPrefix 是 FakeTransport 的地址 scheme，区分于真网络的 ws://、tcp:// 等。
const addrPrefix = "memory://"

// Transport 是 core.Transport 的内存实现。
type Transport struct {
	mu   sync.Mutex
	hubs map[string]*Hub // key = address (去除 scheme)
}

// New 创建新的内存 Transport。
func New() *Transport {
	return &Transport{hubs: make(map[string]*Hub)}
}

// NewHub 在 addr 注册一个新的 FakeHub。
//
// 调用方负责在结束时调用 hub.Close() 来释放资源。
// 注册后即可被 Dial 找到。
func (t *Transport) NewHub(addr string) *Hub {
	if !strings.HasPrefix(addr, addrPrefix) {
		addr = addrPrefix + addr
	}
	trimmed := strings.TrimPrefix(addr, addrPrefix)
	t.mu.Lock()
	defer t.mu.Unlock()
	h := newHub(trimmed)
	t.hubs[trimmed] = h
	return h
}

// ErrNoHub 在 Dial 时找不到对应地址时返回。
var ErrNoHub = errors.New("fake: no hub at that address")

// Dial 模拟客户端拨号到 target 地址。M1 仅按完整地址精确匹配。
//
// ctx 用于取消连接建立过程；ctx 取消后 Hub.dial 返回的 conn 立即被关闭，
// 这是给 Transport 用户的标准约定。
func (t *Transport) Dial(ctx context.Context, target string, hello protocol.Hello) (core.Conn, error) {
	t.mu.Lock()
	addr := strings.TrimPrefix(target, addrPrefix)
	h, ok := t.hubs[addr]
	t.mu.Unlock()
	if !ok {
		return nil, ErrNoHub
	}
	return h.dial(ctx, hello), nil
}

// Listen 是 stub：返回 ctx.Err() 之前一直阻塞，等待 Close。
//
// Hub 由 NewHub 注册并由调用方驱动 Accept，本方法不真正启动 listener。
func (t *Transport) Listen(ctx context.Context, _ string, _ func(core.Conn, protocol.Hello) error) error {
	<-ctx.Done()
	return ctx.Err()
}

// Close 释放 Transport 注册的所有 Hub。
func (t *Transport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, h := range t.hubs {
		h.Close()
	}
}

// Hub 模拟服务端：accept Dial 过来的 Conn，对 FKMessage 分配 ServerSeq、写 Store、按订阅广播。
//
// Hub 不感知业务规则（发言权限、是否群成员等）—— 这些放到 M2 真 Hub。
// M1 只验证"接口契约跑得通"。
type Hub struct {
	addr string

	mu       sync.Mutex
	conns    map[*conn]*peer // 在线设备
	closed   bool
	nextConn int64

	// 已分配且未投递的历史消息冗余备份（断线重连补发用）。
	// key = convID, value = sorted by ServerSeq asc.
	history map[string][]protocol.StoredMessage

	seq   atomic.Uint64 // Hub 端单调序号生成器
	store core.Store    // 落库目标（M1 用 memory.New()）
}

type peer struct {
	hello protocol.Hello
	conn  *conn
}

func newHub(addr string) *Hub {
	return &Hub{
		addr:    addr,
		conns:   make(map[*conn]*peer),
		history: make(map[string][]protocol.StoredMessage),
	}
}

// AttachStore 绑定落库后端。必须在 AcceptConn 之前调用。
func (h *Hub) AttachStore(s core.Store) { h.store = s }

// Accept 由测试代码调用：手动把一个 Conn 注入 Hub，等价于服务端接受了一个连接。
// 之后 Hub 起 goroutine 读该 Conn 上的帧，直到 Conn 关闭。
//
// ctx 用于取消 serveConn：ctx 取消时 Hub 中止该 Conn 的读循环。
func (h *Hub) Accept(ctx context.Context, c core.Conn, hello protocol.Hello) error {
	fc, ok := c.(*conn)
	if !ok {
		return errors.New("fake: Hub.Accept 只能接受 fake.NewConn() 构造的 Conn")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return core.ErrClosed
	}
	h.conns[fc] = &peer{hello: hello, conn: fc}
	h.mu.Unlock()

	go h.serveConn(ctx, fc)
	return nil
}

// dial 是 Transport.Dial 的内部辅助：从客户端视角 new 一个 conn，并立刻交 Hub serveConn。
//
// ctx 用于取消 serveConn：ctx 取消时 Hub 中止该 Conn 的读循环。
func (h *Hub) dial(ctx context.Context, hello protocol.Hello) core.Conn {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return &conn{deviceID: hello.DeviceID, closedCh: closedChan()}
	}
	h.nextConn++
	c := newConn(hello.DeviceID + "-" + itoa(h.nextConn))
	h.conns[c] = &peer{hello: hello, conn: c}
	h.mu.Unlock()
	go h.serveConn(ctx, c)
	return c
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// itoa 把 int64 转 string，避免 fmt 在 hot path 引入开销。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// serveConn 读取该 conn 上的所有帧并按 kind 分发。
//
// 路由表（M1）：
//
//	FKHello       —— 仅做版本兼容检查，错误则发 FKError 关连接（M1 stub）
//	FKMessage     —— 分配 ServerSeq，写 store，append history，给所有 peer 发 FKDeliver
//	FKHistoryReq  —— 读 history[convID]，按 After 分页返回 FKHistoryResp
//	FKAck         —— 写 store 的 cursor（多设备已读同步）
//	FKRead        —— 同 FKRead → cursor
//	FKPing        —— 回 FKPong
//	其它          —— log-and-drop（M1 不实现 Presence/Typing/Error）
func (h *Hub) serveConn(ctx context.Context, c *conn) {
	for {
		// Hub 读客户端送来的帧 → 走 outbound 通道
		f, err := c.readOutbound(ctx)
		if err != nil {
			h.removeConn(c)
			return
		}
		if err := h.handleFrame(ctx, c, f); err != nil {
			h.removeConn(c)
			return
		}
	}
}

func (h *Hub) handleFrame(ctx context.Context, c *conn, f protocol.Frame) error {
	switch f.Kind {
	case protocol.FKHello:
		// 校验协议版本（M1 仅 accept v1，不发 error 即视为通过）
		if len(f.Payload) > 0 {
			var hello protocol.Hello
			if err := json.Unmarshal(f.Payload, &hello); err == nil {
				if hello.ProtocolVersion != protocol.ProtocolVersion {
					h.sendError(ctx, c, protocol.ErrProtocolMismatch, "unsupported protocol version")
					return nil
				}
			}
		}
		return nil

	case protocol.FKMessage:
		var msg protocol.StoredMessage
		if err := json.Unmarshal(f.Payload, &msg); err != nil {
			h.sendError(ctx, c, protocol.ErrInvalidFrame, "bad message payload")
			//nolint:nilerr // sendError 已通知对端，此处保持连接；下一次帧按正常路径处理
			return nil
		}
		seq := h.seq.Add(1)
		msg.ServerSeq = seq
		if h.store != nil {
			_ = h.store.AppendMessage(ctx, msg)
		}
		h.mu.Lock()
		h.history[msg.ConversationID] = append(h.history[msg.ConversationID], msg)
		h.mu.Unlock()
		// 包含发送者：客户端会按 ID 去重，发送者因此能拿到"自己刚发的"event
		h.broadcast(ctx, protocol.Frame{
			Kind:    protocol.FKDeliver,
			Payload: mustJSON(msg),
		})
		return nil

	case protocol.FKHistoryReq:
		var req protocol.HistoryRequest
		if len(f.Payload) > 0 {
			_ = json.Unmarshal(f.Payload, &req)
		}
		resp := h.handleHistoryReq(req)
		_ = c.pushInbound(ctx, protocol.Frame{
			Kind:    protocol.FKHistoryResp,
			Payload: mustJSON(resp),
		})
		return nil

	case protocol.FKAck:
		if h.store == nil {
			return nil
		}
		// Ack 是设备级：f.Ack 是该设备目前已收到的最大 ServerSeq。
		deviceID := deviceIDOf(c)
		if deviceID != "" {
			_ = h.store.SetCursor(ctx, deviceID, "*", f.Ack)
		}
		return nil

	case protocol.FKRead:
		var rd protocol.Read
		if len(f.Payload) > 0 {
			_ = json.Unmarshal(f.Payload, &rd)
		}
		if h.store != nil {
			_ = h.store.SetCursor(ctx, deviceIDOf(c), rd.ConversationID, rd.ServerSeq)
		}
		return nil

	case protocol.FKPing:
		_ = c.pushInbound(ctx, protocol.Frame{Kind: protocol.FKPong})
		return nil

	default:
		// 未实现的 frame kind：静默丢弃（M1 路径足够跑通测试）
		return nil
	}
}

func (h *Hub) handleHistoryReq(req protocol.HistoryRequest) protocol.HistoryResponse {
	if len(req.ConversationIDs) == 1 {
		conv := req.ConversationIDs[0]
		h.mu.Lock()
		list := append([]protocol.StoredMessage(nil), h.history[conv]...)
		h.mu.Unlock()
		return pickAfterLimit(list, req.After, req.Limit)
	}
	// 跨所有会话
	h.mu.Lock()
	var all []protocol.StoredMessage
	for _, list := range h.history {
		all = append(all, list...)
	}
	h.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].ServerSeq < all[j].ServerSeq })
	return pickAfterLimit(all, req.After, req.Limit)
}

func pickAfterLimit(list []protocol.StoredMessage, after uint64, limit int) protocol.HistoryResponse {
	lo := sort.Search(len(list), func(i int) bool { return list[i].ServerSeq > after })
	end := len(list)
	if limit > 0 && lo+limit < end {
		end = lo + limit
	}
	if lo >= end {
		return protocol.HistoryResponse{Messages: nil, HasMore: false}
	}
	out := make([]protocol.StoredMessage, end-lo)
	copy(out, list[lo:end])
	return protocol.HistoryResponse{
		Messages: out,
		HasMore:  end < len(list),
	}
}

func (h *Hub) broadcast(ctx context.Context, f protocol.Frame) {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.pushInbound(ctx, f)
	}
}

func (h *Hub) sendError(ctx context.Context, c *conn, code protocol.ErrorCode, msg string) {
	payload := mustJSON(protocol.ErrorPayload{Code: code, Message: msg})
	_ = c.pushInbound(ctx, protocol.Frame{
		Kind:    protocol.FKError,
		Payload: payload,
	})
}

func (h *Hub) removeConn(c *conn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
	_ = c.Close()
}

func deviceIDOf(c *conn) string {
	if c == nil {
		return ""
	}
	return c.deviceID
}

// ConnCount 返回当前在线 conn 数量，用于测试断言。
func (h *Hub) ConnCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// Close 断开所有 Conn 并标记 Hub 已关闭。
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.conns = nil
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}
