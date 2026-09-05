package client_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	"github.com/pandaymx/lanchat/pkg/transport/fake"
)

// helpers --------------------------------------------------------------------

// newTransportWithHub 创建一个 dial-ready Transport + 一个已 attach store 的 Hub。
// addr 是 Hub 监听的地址字符串。
func newTransportWithHub(t *testing.T, addr string) (*fake.Transport, *fake.Hub, *memory.MemoryStore) {
	t.Helper()
	tr := fake.New()
	hub := tr.NewHub(addr)
	store := memory.New()
	hub.AttachStore(store)
	t.Cleanup(func() {
		hub.Close()
		store.Close()
		tr.Close()
	})
	return tr, hub, store
}

// newClient dial 一次 + Connect + 返回连好的 Client。
func newClient(t *testing.T, tr *fake.Transport, store *memory.MemoryStore, hello protocol.Hello, resumeFrom uint64) *client.Client {
	t.Helper()
	bus := event.New()
	conn, err := tr.Dial(context.Background(), "memory://lanchat-test", hello)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	c := client.New(hello, conn, store, bus)
	if err := c.Connect(context.Background(), client.ConnectOptions{
		ResumeFrom:     resumeFrom,
		RequestHistory: true,
		HistoryLimit:   100,
	}); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// waitForEvent 在 timeout 内等到第一个匹配的 Event，返回 nil 表示超时。
func waitForEvent(t *testing.T, sub core.Subscription, kind core.EventKind, timeout time.Duration) *core.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return nil
			}
			if ev.Kind == kind {
				return &ev
			}
			// skip 不匹配的事件
		case <-deadline:
			return nil
		}
	}
}

// collectEvents 在窗口期内收集所有匹配 kind 的事件。
func collectEvents(t *testing.T, sub core.Subscription, kind core.EventKind, window time.Duration) []core.Event {
	t.Helper()
	var out []core.Event
	deadline := time.After(window)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return out
			}
			if ev.Kind == kind {
				out = append(out, ev)
			}
		case <-deadline:
			return out
		}
	}
}

// TestRoundTrip：alice 发消息 → bob 收到 EventMessage。
// 这是 M1 验收的第一条，也是 ADR-002 的最小证据。
func TestRoundTrip(t *testing.T) {
	tr, hub, store := newTransportWithHub(t, "lanchat-test")

	aliceHello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	}
	bobHello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "bob-laptop",
		UserID:          "bob",
	}

	alice := newClient(t, tr, store, aliceHello, 0)
	bob := newClient(t, tr, store, bobHello, 0)

	// 等连接稳定：bob 的第一个 EventState 出现即视为已就绪。
	bobSub := bob.Subscribe(64)
	defer bobSub.Close()
	if waitForEvent(t, bobSub, core.EventState, 2*time.Second) == nil {
		t.Fatal("bob 历史拉取未启动")
	}

	// alice 发消息
	if err := alice.SendMessage(context.Background(), "conv-1", "hello bob"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// bob 应在 2s 内收到 EventMessage
	ev := waitForEvent(t, bobSub, core.EventMessage, 2*time.Second)
	if ev == nil {
		t.Fatal("bob 未收到 EventMessage")
	}
	if ev.Message == nil {
		t.Fatal("EventMessage payload nil")
	}
	if ev.Message.Body != "hello bob" {
		t.Fatalf("payload mismatch: got %q want %q", ev.Message.Body, "hello bob")
	}
	if ev.ConversationID != "conv-1" {
		t.Fatalf("conv id mismatch: %s", ev.ConversationID)
	}
	if ev.Message.ServerSeq == 0 {
		t.Fatal("ServerSeq 未由 Hub 分配")
	}

	// 验证落库：alice 与 bob 两个 store 都应能查到此消息。
	got, err := alice.History(context.Background(), "conv-1", 0, 10)
	if err != nil {
		t.Fatalf("alice History: %v", err)
	}
	if len(got) != 1 || got[0].Body != "hello bob" {
		t.Fatalf("alice store wrong: %+v", got)
	}
	got, err = bob.History(context.Background(), "conv-1", 0, 10)
	if err != nil {
		t.Fatalf("bob History: %v", err)
	}
	if len(got) != 1 || got[0].Body != "hello bob" {
		t.Fatalf("bob store wrong: %+v", got)
	}

	// 双方共用 hub 的同 store（测试通过 tr 注入同一个 store.New()）
	if hub.ConnCount() != 2 {
		t.Fatalf("hub 应有 2 个连接，实际 %d", hub.ConnCount())
	}
}

// TestOfflineCatchUp：bob 离线时 alice 发 M1/M2，bob 重连后应补发。
func TestOfflineCatchUp(t *testing.T) {
	tr, _, store := newTransportWithHub(t, "lanchat-test")

	bobHello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "bob-laptop",
		UserID:          "bob",
	}
	aliceHello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	}

	// 阶段 1：bob 在线，发一条并正常接收，cursor 推进到 ServerSeq>=1
	aliceA := newClient(t, tr, store, aliceHello, 0)
	bobA := newClient(t, tr, store, bobHello, 0)
	bobSubA := bobA.Subscribe(64)
	defer bobSubA.Close()
	_ = waitForEvent(t, bobSubA, core.EventState, 2*time.Second)

	if err := aliceA.SendMessage(context.Background(), "c1", "M0-baseline"); err != nil {
		t.Fatalf("baseline send: %v", err)
	}
	m := waitForEvent(t, bobSubA, core.EventMessage, 2*time.Second)
	if m == nil {
		t.Fatal("baseline M0 未送达 bob")
	}
	if err := bobA.SendRead(context.Background(), "c1", m.Message.ServerSeq); err != nil {
		t.Fatalf("SendRead: %v", err)
	}

	// 阶段 2：bob 离线。Close 后 hub 应发现连接断开。
	if err := bobA.Close(); err != nil {
		t.Fatalf("bobA close: %v", err)
	}

	// 给 alice 一个新的 client 继续发言，模拟"alice 是离线观察者"的反例不存在。
	// 由于 aliceStoreB 没必要独立，本测试让 aliceB 共用同一 store，证明"bob 离线不影响 alice 端落库"。
	aliceB := newClient(t, tr, store, aliceHello, 0)
	if err := aliceB.SendMessage(context.Background(), "c1", "M1-while-offline"); err != nil {
		t.Fatalf("aliceB M1: %v", err)
	}
	if err := aliceB.SendMessage(context.Background(), "c1", "M2-while-offline"); err != nil {
		t.Fatalf("aliceB M2: %v", err)
	}

	// 阶段 3：bob 用相同 cursor 重连，期望历史补发 M1+M2（且不重复 M0）。
	bobStoreB := memory.New()
	t.Cleanup(func() { bobStoreB.Close() })

	busB := event.New()
	conn2, err := tr.Dial(context.Background(), "memory://lanchat-test", bobHello)
	if err != nil {
		t.Fatalf("bob redial: %v", err)
	}
	bobB := client.New(bobHello, conn2, bobStoreB, busB)
	if err := bobB.Connect(context.Background(), client.ConnectOptions{
		ResumeFrom:     m.Message.ServerSeq, // = 1，期望补发 >1 的
		RequestHistory: true,
		HistoryLimit:   100,
	}); err != nil {
		t.Fatalf("bobB Connect: %v", err)
	}
	t.Cleanup(func() { _ = bobB.Close() })

	bobSubB := bobB.Subscribe(64)
	defer bobSubB.Close()

	// 收集所有 EventMessage
	evs := collectEvents(t, bobSubB, core.EventMessage, 2*time.Second)
	if len(evs) < 2 {
		t.Fatalf("期望 ≥2 个补发事件，实际 %d", len(evs))
	}
	bodies := make([]string, 0, len(evs))
	for _, e := range evs {
		if e.Message != nil {
			bodies = append(bodies, e.Message.Body)
		}
	}
	if evs[0].Message == nil || evs[0].Message.Body != "M1-while-offline" ||
		evs[1].Message == nil || evs[1].Message.Body != "M2-while-offline" {
		t.Fatalf("补发顺序或内容错误: bodies=%v", bodies)
	}

	// 验证：bob 自己的 store 现在应当已经收到补发（M1+M2），M0 因为 bobB 是新 store 不存在。
	got, _ := bobB.History(context.Background(), "c1", 0, 100)
	if len(got) != 2 {
		t.Fatalf("bob 落库消息数错误: 期望 2 (M1+M2)，实际 %d", len(got))
	}
	if got[0].Body != "M1-while-offline" || got[1].Body != "M2-while-offline" {
		t.Fatalf("bob 补发顺序/内容错误: %+v", got)
	}
}

// TestSubscribeFilter：业务侧订阅应被 EventBus 正确过滤到 conv-1 的事件。
// 当前 EventBus 接受全部事件，过滤责任在业务层；本测试断言业务侧的纯过滤路径正确。
func TestSubscribeFilter(t *testing.T) {
	tr, _, store := newTransportWithHub(t, "lanchat-test")

	helloAlice := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "alice-d1",
		UserID:          "alice",
	}
	helloBob := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "bob-d1",
		UserID:          "bob",
	}

	alice := newClient(t, tr, store, helloAlice, 0)
	sub := alice.Subscribe(64)
	defer sub.Close()

	// 等 readPump 启动
	_ = waitForEvent(t, sub, core.EventState, 2*time.Second)

	bob := newClient(t, tr, store, helloBob, 0)
	bobSub := bob.Subscribe(64)
	defer bobSub.Close()
	_ = waitForEvent(t, bobSub, core.EventState, 2*time.Second)

	// alice 发到 conv-1 与 conv-2
	if err := alice.SendMessage(context.Background(), "conv-1", "in-conv-1"); err != nil {
		t.Fatalf("send conv-1: %v", err)
	}
	if err := alice.SendMessage(context.Background(), "conv-2", "in-conv-2"); err != nil {
		t.Fatalf("send conv-2: %v", err)
	}

	// bob 应收到两条；alice 仅收自己发的（hub 也回 FKDeliver 给发送方）
	allAlice := collectEvents(t, sub, core.EventMessage, 2*time.Second)
	if len(allAlice) != 2 {
		t.Fatalf("alice EventMessage 期望 2，实际 %d", len(allAlice))
	}

	// 业务侧过滤：仅保留 conv-1
	var aliceConv1 []core.Event
	for _, e := range allAlice {
		if e.ConversationID == "conv-1" && e.Message != nil && e.Message.Body == "in-conv-1" {
			aliceConv1 = append(aliceConv1, e)
		}
	}
	if len(aliceConv1) != 1 {
		t.Fatalf("alice 过滤 conv-1 应为 1 条，实际 %d", len(aliceConv1))
	}

	// bob：等待两条
	allBob := collectEvents(t, bobSub, core.EventMessage, 2*time.Second)
	if len(allBob) != 2 {
		t.Fatalf("bob EventMessage 期望 2，实际 %d", len(allBob))
	}

	// 不阻塞，并发 sanity
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = alice.SendMessage(context.Background(), "conv-1", "burst")
		}()
	}
	wg.Wait()
}

// TestMultiDeviceCursor：alice 在两台设备上独立维护 cursor。
// 这是 ADR-008 的核心验收：多设备**不被踢下线**，游标按设备独立。
func TestMultiDeviceCursor(t *testing.T) {
	tr, _, store := newTransportWithHub(t, "lanchat-test")

	helloDev1 := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "alice-d1",
		UserID:          "alice",
	}
	helloDev2 := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "alice-d2",
		UserID:          "alice",
	}

	d1 := newClient(t, tr, store, helloDev1, 0)
	d2 := newClient(t, tr, store, helloDev2, 0)

	// 都连上 hub
	sub1 := d1.Subscribe(64)
	defer sub1.Close()
	sub2 := d2.Subscribe(64)
	defer sub2.Close()
	_ = waitForEvent(t, sub1, core.EventState, 2*time.Second)
	_ = waitForEvent(t, sub2, core.EventState, 2*time.Second)

	// 制造 5 条消息
	for i := 0; i < 5; i++ {
		if err := d1.SendMessage(context.Background(), "c1", "M"); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	// d1 收到自己发的 + 自己 echo（hub 广播），但 d1 也算一个接收方。
	// 收集完毕
	_ = collectEvents(t, sub1, core.EventMessage, 1*time.Second)
	got2 := collectEvents(t, sub2, core.EventMessage, 1*time.Second)
	if len(got2) != 5 {
		t.Fatalf("d2 期望收 5 条消息，实际 %d", len(got2))
	}

	// 取最后一条 serverSeq
	lastSeq := got2[len(got2)-1].Message.ServerSeq

	// d2 标记整段 conv 已读，但 d1 不动
	if err := d2.SendRead(context.Background(), "c1", lastSeq); err != nil {
		t.Fatalf("d2 SendRead: %v", err)
	}

	// d2 cursor 推进，d1 cursor 应仍是 0
	c2, err := d2.Cursor(context.Background(), "c1")
	if err != nil {
		t.Fatalf("d2 cursor: %v", err)
	}
	if c2 != lastSeq {
		t.Fatalf("d2 cursor 应为 %d，实际 %d", lastSeq, c2)
	}
	c1, err := d1.Cursor(context.Background(), "c1")
	if err != nil {
		t.Fatalf("d1 cursor: %v", err)
	}
	if c1 != 0 {
		t.Fatalf("d1 cursor 应保持 0（未读），实际 %d", c1)
	}

	// 验证 d1 自己 mark-read 部分（cursor 到 seq3）
	third := got2[2].Message.ServerSeq
	if err := d1.SendRead(context.Background(), "c1", third); err != nil {
		t.Fatalf("d1 SendRead: %v", err)
	}
	c1, _ = d1.Cursor(context.Background(), "c1")
	if c1 != third {
		t.Fatalf("d1 cursor partial 应为 %d，实际 %d", third, c1)
	}
	// d2 应不受影响
	c2, _ = d2.Cursor(context.Background(), "c1")
	if c2 != lastSeq {
		t.Fatalf("d2 cursor 仍应为 %d，实际 %d", lastSeq, c2)
	}
}
