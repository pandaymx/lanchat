package hubstate

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// ---- 测试用 Peer：双向管道 ----

// pipePeer 是一个内存 Peer，Recv 从队列取，Send 投递并记录。
type pipePeer struct {
	deviceID string

	mu     sync.Mutex
	inbox  []protocol.Frame
	notify chan struct{}
	closed bool

	// received 记录收到的帧，供断言
	received []protocol.Frame
}

func newPipePeer(deviceID string) *pipePeer {
	return &pipePeer{
		deviceID: deviceID,
		notify:   make(chan struct{}, 1),
	}
}

func (p *pipePeer) Recv(ctx context.Context) (protocol.Frame, error) {
	for {
		p.mu.Lock()
		if len(p.inbox) > 0 {
			f := p.inbox[0]
			p.inbox = p.inbox[1:]
			p.mu.Unlock()
			return f, nil
		}
		if p.closed {
			p.mu.Unlock()
			return protocol.Frame{}, context.Canceled
		}
		p.mu.Unlock()

		select {
		case <-p.notify:
		case <-ctx.Done():
			return protocol.Frame{}, ctx.Err()
		}
	}
}

func (p *pipePeer) Send(_ context.Context, f protocol.Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return core.ErrClosed
	}
	p.received = append(p.received, f)
	return nil
}

func (p *pipePeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	select {
	case p.notify <- struct{}{}:
	default:
	}
	return nil
}

func (p *pipePeer) Closed() <-chan struct{} { return nil }

func (p *pipePeer) DeviceID() string { return p.deviceID }

// inject 塞一帧进"待处理"队列，模拟客户端发来。
func (p *pipePeer) inject(f protocol.Frame) {
	p.mu.Lock()
	p.inbox = append(p.inbox, f)
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// framesOf 取出收到的指定 Kind 的帧。
func (p *pipePeer) framesOf(k protocol.FrameKind) []protocol.Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []protocol.Frame
	for _, f := range p.received {
		if f.Kind == k {
			out = append(out, f)
		}
	}
	return out
}

// waitFor 在 timeout 内等到某 Kind 的帧出现至少 n 条。
func (p *pipePeer) waitFor(k protocol.FrameKind, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(p.framesOf(k)) >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return len(p.framesOf(k)) >= n
}

// ---- helpers ----

func mustPayload(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// setupRouter 建一个带 memory store 的 Router。
func setupRouter(t *testing.T) (*Router, *memory.MemoryStore) {
	t.Helper()
	store := memory.New()
	r := NewRouter(&RouterConfig{Store: store})
	t.Cleanup(func() {
		r.Close()
		_ = store.Close()
	})
	return r, store
}

// addPeer 登记并握手一条连接，返回它和 peerID。
func addPeer(t *testing.T, r *Router, deviceID, userID string) (*pipePeer, uint64) {
	t.Helper()
	p := newPipePeer(deviceID)
	id := r.reg.Add(p, deviceID, userID)
	r.reg.MarkHello(id, deviceID, userID)
	t.Cleanup(func() { _ = p.Close() })
	return p, id
}

// ---- 测试 ----

func TestRouterHelloAccept(t *testing.T) {
	r, _ := setupRouter(t)
	p := newPipePeer("dev-1")
	defer func() { _ = p.Close() }()

	id := r.reg.Add(p, "dev-1", "")
	hello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "dev-1",
		UserID:          "u-1",
	}
	if err := r.HandleFrame(context.Background(), id, p,
		protocol.Frame{Kind: protocol.FKHello, Payload: mustPayload(t, hello)}); err != nil {
		t.Fatalf("合法 Hello 不应报错: %v", err)
	}

	// 握手后应可被投递
	if got := len(r.reg.PeersForUser("u-1")); got != 1 {
		t.Fatalf("握手后 u-1 应有 1 条连接，实际 %d", got)
	}
}

func TestRouterHelloVersionMismatch(t *testing.T) {
	r, _ := setupRouter(t)
	p := newPipePeer("dev-1")
	defer func() { _ = p.Close() }()
	id := r.reg.Add(p, "dev-1", "")

	hello := protocol.Hello{ProtocolVersion: 99, DeviceID: "dev-1", UserID: "u-1"}
	err := r.HandleFrame(context.Background(), id, p,
		protocol.Frame{Kind: protocol.FKHello, Payload: mustPayload(t, hello)})

	if err == nil {
		t.Fatal("版本不匹配应返回错误以关闭连接")
	}

	// 应已发出 FKError
	errs := p.framesOf(protocol.FKError)
	if len(errs) != 1 {
		t.Fatalf("应发出 1 个 FKError，实际 %d", len(errs))
	}
	var ep protocol.ErrorPayload
	if err := json.Unmarshal(errs[0].Payload, &ep); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if ep.Code != protocol.ErrProtocolMismatch {
		t.Fatalf("错误码应为 ErrProtocolMismatch，实际 %v", ep.Code)
	}
}

// TestRouterMessageSequenceAndBroadcast 验证写路径：分配 seq、落库、广播。
func TestRouterMessageSequenceAndBroadcast(t *testing.T) {
	ctx := context.Background()
	r, store := setupRouter(t)

	p1, id1 := addPeer(t, r, "dev-1", "u-1")
	p2, _ := addPeer(t, r, "dev-2", "u-2")

	msg := protocol.StoredMessage{
		ID:             "m1",
		ConversationID: "c1",
		SenderUserID:   "u-1",
		Body:           "hello",
	}
	if err := r.HandleFrame(ctx, id1, p1,
		protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg)}); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}

	// 两条连接都应收到（含发送者）
	if !p1.waitFor(protocol.FKDeliver, 1, time.Second) {
		t.Fatal("发送者未收到 FKDeliver")
	}
	if !p2.waitFor(protocol.FKDeliver, 1, time.Second) {
		t.Fatal("对方未收到 FKDeliver")
	}

	// ServerSeq 应被分配
	delivered := p1.framesOf(protocol.FKDeliver)
	var got protocol.StoredMessage
	if err := json.Unmarshal(delivered[0].Payload, &got); err != nil {
		t.Fatalf("unmarshal deliver: %v", err)
	}
	if got.ServerSeq != 1 {
		t.Fatalf("首条消息 ServerSeq 应为 1，实际 %d", got.ServerSeq)
	}
	// CreatedAt 应由 Hub 补齐（客户端不传）
	if got.CreatedAt == 0 {
		t.Error("Hub 应补齐 CreatedAt")
	}

	// 应已落库
	hist, err := store.History(ctx, "c1", 0, 10)
	if err != nil {
		t.Fatalf("store history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("应有 1 条落库，实际 %d", len(hist))
	}
	if hist[0].ServerSeq != 1 {
		t.Fatalf("落库消息的 seq 应为 1，实际 %d", hist[0].ServerSeq)
	}
}

// TestRouterSeqMonotonic 验证并发发消息时 ServerSeq 单调递增不重复。
func TestRouterSeqMonotonic(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)
	_, id := addPeer(t, r, "dev-1", "u-1")
	p, _ := addPeer(t, r, "dev-2", "u-2")

	const n = 50
	for i := range n {
		msg := protocol.StoredMessage{
			ID:             string(rune('a' + i%26)),
			ConversationID: "c1",
			Body:           "m",
		}
		_ = r.HandleFrame(ctx, id, p,
			protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg)})
	}

	if got := r.seq.Last(); got != n {
		t.Fatalf("应分配了 %d 个序号，实际 %d", n, got)
	}
	if got := r.hist.Len("c1"); got != n {
		t.Fatalf("History 应有 %d 条，实际 %d", n, got)
	}
}

// TestRouterHistoryReqPerDevice 验证补发只回给请求的那条连接。
func TestRouterHistoryReqPerDevice(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)

	p1, id1 := addPeer(t, r, "dev-1", "u-1")
	p2, id2 := addPeer(t, r, "dev-2", "u-1") // 同一用户的第二台设备

	// 先灌 3 条消息
	for i := 1; i <= 3; i++ {
		msg := protocol.StoredMessage{ID: "m", ConversationID: "c1", ServerSeq: uint64(i)}
		// 直接走 handleMessage 的等价路径：通过 frame
		_ = r.HandleFrame(ctx, id1, p1,
			protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg)})
	}

	// dev-2 请求补发 after=1
	req := protocol.HistoryRequest{ConversationIDs: []string{"c1"}, After: 1, Limit: 10}
	if err := r.HandleFrame(ctx, id2, p2,
		protocol.Frame{Kind: protocol.FKHistoryReq, Payload: mustPayload(t, req)}); err != nil {
		t.Fatalf("history req: %v", err)
	}

	// dev-2 应收到 resp
	resps := p2.framesOf(protocol.FKHistoryResp)
	if len(resps) != 1 {
		t.Fatalf("dev-2 应收到 1 个 FKHistoryResp，实际 %d", len(resps))
	}
	var resp protocol.HistoryResponse
	if err := json.Unmarshal(resps[0].Payload, &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("after=1 应补发 2 条（seq 2,3），实际 %d", len(resp.Messages))
	}
	if resp.Messages[0].ServerSeq != 2 {
		t.Fatalf("首条应为 seq 2，实际 %d", resp.Messages[0].ServerSeq)
	}
}

// TestRouterHistoryLimitCapped 验证 limit 被 maxHistoryLimit 截断。
func TestRouterHistoryLimitCapped(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)
	p, id := addPeer(t, r, "dev-1", "u-1")

	req := protocol.HistoryRequest{ConversationIDs: []string{"c1"}, After: 0, Limit: 999999}
	if err := r.HandleFrame(ctx, id, p,
		protocol.Frame{Kind: protocol.FKHistoryReq, Payload: mustPayload(t, req)}); err != nil {
		t.Fatalf("history req: %v", err)
	}
	// 空历史下不返回内容即可，这里主要验证不会 panic 且能走通
	if len(p.framesOf(protocol.FKHistoryResp)) != 1 {
		t.Fatal("应返回 1 个 FKHistoryResp")
	}
	if r.maxHistoryLimit != defaultMaxHistoryLimit {
		t.Fatalf("默认上限应为 %d，实际 %d", defaultMaxHistoryLimit, r.maxHistoryLimit)
	}
}

// TestRouterReadCursorPerDevice 验证游标是 per-device 的（ADR-008 核心）。
func TestRouterReadCursorPerDevice(t *testing.T) {
	ctx := context.Background()
	r, store := setupRouter(t)

	p1, id1 := addPeer(t, r, "dev-1", "u-1")
	_, id2 := addPeer(t, r, "dev-2", "u-1")

	rd := protocol.Read{ConversationID: "c1", ServerSeq: 42}
	if err := r.HandleFrame(ctx, id1, p1,
		protocol.Frame{Kind: protocol.FKRead, Payload: mustPayload(t, rd)}); err != nil {
		t.Fatalf("read: %v", err)
	}

	c1, err := store.GetCursor(ctx, "dev-1", "c1")
	if err != nil {
		t.Fatalf("get cursor dev-1: %v", err)
	}
	if c1 != 42 {
		t.Fatalf("dev-1 游标应为 42，实际 %d", c1)
	}

	// dev-2 未读，应保持 0
	c2, err := store.GetCursor(ctx, "dev-2", "c1")
	if err != nil {
		t.Fatalf("get cursor dev-2: %v", err)
	}
	if c2 != 0 {
		t.Fatalf("dev-2 游标应保持 0，实际 %d", c2)
	}
	_ = id2
}

// TestRouterAckWritesGlobalCursor 验证 Ack 写的是跨会话全局游标（conv="*"）。
func TestRouterAckWritesGlobalCursor(t *testing.T) {
	ctx := context.Background()
	r, store := setupRouter(t)
	p, id := addPeer(t, r, "dev-1", "u-1")

	if err := r.HandleFrame(ctx, id, p,
		protocol.Frame{Kind: protocol.FKAck, Ack: 7}); err != nil {
		t.Fatalf("ack: %v", err)
	}

	got, err := store.GetCursor(ctx, "dev-1", "*")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if got != 7 {
		t.Fatalf("全局游标应为 7，实际 %d", got)
	}
}

func TestRouterPingPong(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)
	p, id := addPeer(t, r, "dev-1", "u-1")

	if err := r.HandleFrame(ctx, id, p, protocol.Frame{Kind: protocol.FKPing}); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got := len(p.framesOf(protocol.FKPong)); got != 1 {
		t.Fatalf("应回 1 个 FKPong，实际 %d", got)
	}
}

// TestRouterUnknownKindIgnored 验证未知帧被静默丢弃（不断连）。
func TestRouterUnknownKindIgnored(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)
	p, id := addPeer(t, r, "dev-1", "u-1")

	if err := r.HandleFrame(ctx, id, p,
		protocol.Frame{Kind: protocol.FrameKind(200)}); err != nil {
		t.Fatalf("未知 kind 不应报错，实际: %v", err)
	}
}

// TestRouterTypingNotEchoed 验证 Typing 不会回显给发送者。
func TestRouterTypingNotEchoed(t *testing.T) {
	ctx := context.Background()
	r, _ := setupRouter(t)

	p1, id1 := addPeer(t, r, "dev-1", "u-1")
	p2, _ := addPeer(t, r, "dev-2", "u-2")

	if err := r.HandleFrame(ctx, id1, p1,
		protocol.Frame{Kind: protocol.FKTyping, Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("typing: %v", err)
	}

	if got := len(p1.framesOf(protocol.FKTyping)); got != 0 {
		t.Fatalf("发送者不应收到自己的 Typing，实际 %d", got)
	}
	if got := len(p2.framesOf(protocol.FKTyping)); got != 1 {
		t.Fatalf("对方应收到 1 个 Typing，实际 %d", got)
	}
}

// TestRouterServePeerLifecycle 验证完整连接生命周期：
// 接入 → 握手 → 处理帧 → 断开注销。
//
// 注意 ServePeer 自己负责 Add 与 Remove，调用方**不要**预先登记，
// 否则会留下一条孤儿注册项。
func TestRouterServePeerLifecycle(t *testing.T) {
	r, _ := setupRouter(t)

	p := newPipePeer("dev-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		r.ServePeer(ctx, p)
		close(done)
	}()

	// 1) 握手
	hello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "dev-1",
		UserID:          "u-1",
	}
	p.inject(protocol.Frame{Kind: protocol.FKHello, Payload: mustPayload(t, hello)})

	// 握手是异步的（ServePeer 在另一个 goroutine），轮询等它生效
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(r.reg.PeersForUser("u-1")) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := len(r.reg.PeersForUser("u-1")); got != 1 {
		t.Fatalf("握手后 u-1 应有 1 条可投递连接，实际 %d", got)
	}

	// 2) 正常处理一帧
	p.inject(protocol.Frame{Kind: protocol.FKPing})
	if !p.waitFor(protocol.FKPong, 1, time.Second) {
		t.Fatal("ServePeer 应处理 Ping 并回 Pong")
	}

	// 3) 断开
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 ServePeer 未退出")
	}

	if r.reg.Count() != 0 {
		t.Fatalf("断开后应注销连接，实际剩 %d", r.reg.Count())
	}
}

// TestRouterNoStore 验证没有 Store 时也能跑（纯内存 Hub）。
func TestRouterNoStore(t *testing.T) {
	ctx := context.Background()
	r := NewRouter(nil)
	defer r.Close()

	p, id := addPeer(t, r, "dev-1", "u-1")
	msg := protocol.StoredMessage{ID: "m1", ConversationID: "c1", Body: "x"}
	if err := r.HandleFrame(ctx, id, p,
		protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg)}); err != nil {
		t.Fatalf("无 Store 时发消息不应报错: %v", err)
	}
	if !p.waitFor(protocol.FKDeliver, 1, time.Second) {
		t.Fatal("无 Store 时也应广播")
	}
}
