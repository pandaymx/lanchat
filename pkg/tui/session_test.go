package tui_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	"github.com/pandaymx/lanchat/pkg/transport/fake"
	"github.com/pandaymx/lanchat/pkg/tui"
)

// helpers -----------------------------------------------------------------

// newFakeBus 在 addr 注册一个 fake Hub 并返回 transport。
//
// 端到端测试都用单一 hub + 两端 Session 同时 dial 的模式：避免引入 client
// 包的辅助 helper，保持 tui_test 的依赖面收敛在 tui + fake + core。
//
// store 必须 attach：hubstate.Router 在握手时要查 store（FKHistory 等），
// 没 attach 直接走 nil-store Router 会让初始 FKDeliver 链路断掉。
func newFakeBus(t *testing.T, addr string) *fake.Transport {
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
	return tr
}

// Tests -------------------------------------------------------------------

// TestDial_RequiresTransport 验证 Transport 缺省时 Dial 快速报错，
// 而不是等到 TCP 超时才给提示。
func TestDial_RequiresTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := tui.Dial(ctx, tui.DialOptions{HubURL: "memory://x"})
	if err == nil {
		t.Fatal("Dial should fail when Transport is nil")
	}
}

// TestDial_ConvIDDefault 验证 ConvID 留空时回落到 DefaultConversationID。
func TestDial_ConvIDDefault(t *testing.T) {
	tr := newFakeBus(t, "memory://conv-default")
	sess, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://conv-default",
		User:      "alice",
		Device:    "lap",
		// ConvID 不传
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()
	if got := sess.ConversationID(); got != tui.DefaultConversationID {
		t.Fatalf("ConvID should default to %q, got %q", tui.DefaultConversationID, got)
	}
}

// TestSession_SendDeliversToPeer 验证 Session.Send 经 fake hub 端到端到达对端。
// 这是 M3.5 的核心契约：Model.submitMsg → Sender.Send → core.Event → Model.Publish。
func TestSession_SendDeliversToPeer(t *testing.T) {
	tr := newFakeBus(t, "memory://send-roundtrip")

	alice, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://send-roundtrip",
		User:      "alice",
		Device:    "alice-laptop",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial alice: %v", err)
	}
	defer alice.Close()

	bob, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://send-roundtrip",
		User:      "bob",
		Device:    "bob-laptop",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial bob: %v", err)
	}
	defer bob.Close()

	// bob 起 Pump 收集事件
	var (
		mu     sync.Mutex
		seenEv []core.Event
	)
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	defer pumpCancel()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		bob.Pump(pumpCtx, func(e core.Event) {
			mu.Lock()
			seenEv = append(seenEv, e)
			mu.Unlock()
		})
	}()

	// 等 bob 的 EventState（连接就绪）再发；如果等不到就报错。
	if !waitFirstKind(t, &mu, &seenEv, []core.EventKind{core.EventState}, 2*time.Second) {
		mu.Lock()
		var dump []string
		for _, e := range seenEv {
			dump = append(dump, e.Kind.String())
		}
		mu.Unlock()
		t.Fatalf("bob Pump 未转发 EventState，看到 [%s]", strings.Join(dump, ","))
	}

	if err := alice.Send(context.Background(), "hello bob from m3.5"); err != nil {
		t.Fatalf("Session.Send: %v", err)
	}

	if !waitFirstMessage(t, &mu, &seenEv, "hello bob from m3.5", 3*time.Second) {
		t.Fatal("bob 未收到 alice 发出的消息")
	}
}

// TestSession_PumpExitsOnContextCancel 验证 ctx 取消后 Pump 立即返回。
//
// 不能用 `for range Events()` 写法跑：pkg/event 的 sub.Close() 不关闭 channel，
// range 会永久阻塞。退出条件必须显式来自 ctx.Done / Client.Done / Session.Done。
func TestSession_PumpExitsOnContextCancel(t *testing.T) {
	tr := newFakeBus(t, "memory://pump-cancel")

	sess, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://pump-cancel",
		User:      "alice",
		Device:    "lap",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()

	pumpCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess.Pump(pumpCtx, func(core.Event) {})
	}()

	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Pump 仍未退出")
	}
}

// TestSession_CloseIsIdempotent 验证 Close 幂等。
func TestSession_CloseIsIdempotent(t *testing.T) {
	tr := newFakeBus(t, "memory://close-idempotent")

	sess, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://close-idempotent",
		User:      "alice",
		Device:    "lap",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// 二次 Close 不应 panic，也不应累计错误（实现用了 sync.Once）。
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestSession_SendAfterCloseReturnsError 验证连接关闭后发送立刻报错，
// 而不是让 goroutine 永远挂在 SendMessage 的 write 等待上。
func TestSession_SendAfterCloseReturnsError(t *testing.T) {
	tr := newFakeBus(t, "memory://send-after-close")

	sess, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://send-after-close",
		User:      "alice",
		Device:    "lap",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sess.Send(ctx, "no can do"); err == nil {
		t.Fatal("Send on closed Session should return error")
	}
}

// TestSession_DroppedEventIsSafe 验证 Pump 阶段 sink 阻塞/panic 都不会让
// 事件循环僵死——此处只验证 nil sink 立即返回。
func TestSession_DroppedEventIsSafe(t *testing.T) {
	tr := newFakeBus(t, "memory://nil-sink")

	sess, err := tui.Dial(context.Background(), tui.DialOptions{
		Transport: tr,
		HubURL:    "memory://nil-sink",
		User:      "alice",
		Device:    "lap",
		ConvID:    "lobby",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()

	pumpCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pump 应在 sink==nil 时立刻返回，不挂起。
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess.Pump(pumpCtx, nil)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Fatal("Pump with nil sink should return immediately")
	}
}

// shared helpers -------------------------------------------------------------

// waitFirstKind 轮询直到 sink 收到任意 kind 中的一个。
//
// seenEv 通过指针传：每次 Pump append 后我们都得看到最新长度，
// slice header 是值语义，必须用 *[]Event。
func waitFirstKind(t *testing.T, mu *sync.Mutex, seenEv *[]core.Event, kinds []core.EventKind, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range *seenEv {
			for _, k := range kinds {
				if e.Kind == k {
					mu.Unlock()
					return true
				}
			}
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitFirstMessage 轮询直到 sink 收到 body 等于 text 的 EventMessage。
func waitFirstMessage(t *testing.T, mu *sync.Mutex, seenEv *[]core.Event, text string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range *seenEv {
			if e.Kind == core.EventMessage && e.Message != nil && e.Message.Body == text {
				mu.Unlock()
				return true
			}
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// keep referenced imports compile-clean when test list varies.
var _ = protocol.Hello{}
