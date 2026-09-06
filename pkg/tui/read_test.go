package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakeReader 同时实现 Sender 与 Reader（M8.1），记录 SendRead 的 seq。
// 线程安全：readCmd 在 tea 运行时子命令 goroutine 里执行（-race 下会
// 与轮询断言并发），所以 SendRead 与 seqs 都要加锁。
type fakeReader struct {
	mu           sync.Mutex
	sendReadSeqs []uint64
}

func (f *fakeReader) Send(context.Context, string) error { return nil }

func (f *fakeReader) SendRead(_ context.Context, seq uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendReadSeqs = append(f.sendReadSeqs, seq)
	return nil
}

func (f *fakeReader) seqs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint64, len(f.sendReadSeqs))
	copy(out, f.sendReadSeqs)
	return out
}

// TestRead_IncomingMarksOwnMessages 验证他人已读回执让「自己发、被读到」
// 的消息渲染出 ✓已读；他人消息 / 未被读到的自己消息不渲染。
func TestRead_IncomingMarksOwnMessages(t *testing.T) {
	m := New(Config{User: "alice", Device: "dev-alice"})
	setSize(m)

	// alice 发 seq=5（自己消息）、bob 发 seq=6（他人消息）。
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m5", ServerSeq: 5, SenderUserID: "alice", Body: "hi", CreatedAt: 1700000000000,
	}})
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m6", ServerSeq: 6, SenderUserID: "bob", Body: "yo", CreatedAt: 1700000000001,
	}})

	// 未收到任何已读回执：自己消息也不应带标记。
	if v := m.View().Content; strings.Contains(v, "✓") {
		t.Fatalf("未收到已读回执前不应渲染标记，实际 %q", v)
	}

	// bob 的设备读到 seq=6：alice 的 m5（seq<=6）应出现 ✓已读；bob 的 m6 不标。
	m.applyEvent(core.Event{Kind: core.EventRead, Read: &protocol.ReadCursor{
		UserID: "bob", DeviceID: "dev-bob", ConversationID: "lobby", ServerSeq: 6,
	}})
	v := m.View().Content
	if !strings.Contains(v, "✓") {
		t.Fatalf("他人已读后自己的消息应带 ✓已读，实际 %q", v)
	}
	if got := strings.Count(v, "✓"); got != 1 {
		t.Fatalf("只有自己的消息应被标记，实际标记数 %d（内容 %q）", got, v)
	}
}

// TestRead_MonotonicMerge 验证同设备旧游标（乱序帧）不把已读状态拉回。
func TestRead_MonotonicMerge(t *testing.T) {
	m := New(Config{User: "alice", Device: "dev-alice"})
	setSize(m)

	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m5", ServerSeq: 5, SenderUserID: "alice", Body: "hi", CreatedAt: 1700000000000,
	}})
	// 先到 seq=10，再到 seq=4：upsertRead 单调合并，标记保持 10 的语义。
	m.applyEvent(core.Event{Kind: core.EventRead, Read: &protocol.ReadCursor{DeviceID: "dev-bob", ServerSeq: 10}})
	m.applyEvent(core.Event{Kind: core.EventRead, Read: &protocol.ReadCursor{DeviceID: "dev-bob", ServerSeq: 4}})
	if !strings.Contains(m.View().Content, "✓") {
		t.Fatal("旧游标不应把已读标记拉回")
	}
}

// TestRead_OwnCursorIgnored 验证自己设备的回执（异常帧）被忽略。
func TestRead_OwnCursorIgnored(t *testing.T) {
	m := New(Config{User: "alice", Device: "dev-alice"})
	setSize(m)
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m5", ServerSeq: 5, SenderUserID: "alice", Body: "hi", CreatedAt: 1700000000000,
	}})
	m.applyEvent(core.Event{Kind: core.EventRead, Read: &protocol.ReadCursor{DeviceID: "dev-alice", ServerSeq: 5}})
	if strings.Contains(m.View().Content, "✓") {
		t.Fatal("自己设备的已读回执不应渲染标记")
	}
}

// TestRead_OutgoingOnBottomMessage 验证他人新消息到达且贴底时上发已读回执。
func TestRead_OutgoingOnBottomMessage(t *testing.T) {
	f := &fakeReader{}
	m := New(Config{User: "alice", Device: "dev-alice", Sender: f})
	// WindowSizeMsg 给内层 viewport 设尺寸：历史短到无需滚动 → AtBottom=true。
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// 首屏历史：自己的消息在底（用户贴底）。
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m5", ServerSeq: 5, SenderUserID: "alice", Body: "hi", CreatedAt: 1700000000000,
	}})

	// bob 新消息到达且贴底 → Update 返回 BatchMsg（listenCmd + readCmd）。
	_, cmd := m.Update(eventMsg{event: core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m6", ServerSeq: 6, SenderUserID: "bob", Body: "yo", CreatedAt: 1700000000001,
	}}})
	if cmd == nil {
		t.Fatal("贴底收到他人消息应返回含 readCmd 的 batch")
	}
	runCmdSafe(t, cmd)
	waitRead(t, f, 6)
}

// TestRead_EndKeySendsReceipt 验证 End 键（跳到尾部）上发已读回执。
func TestRead_EndKeySendsReceipt(t *testing.T) {
	f := &fakeReader{}
	m := New(Config{User: "alice", Device: "dev-alice", Sender: f})
	setSize(m)
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m5", ServerSeq: 5, SenderUserID: "bob", Body: "yo", CreatedAt: 1700000000000,
	}})

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if cmd == nil {
		t.Fatal("End 键应返回 readCmd")
	}
	runCmdSafe(t, cmd)
	waitRead(t, f, 5)
}

// TestRead_NoReaderNoop 验证 sender 不实现 Reader 时贴底收消息不 panic、不上发。
func TestRead_NoReaderNoop(t *testing.T) {
	m := New(Config{User: "alice", Device: "dev-alice", Sender: &fakeSender{}})
	setSize(m)
	_, cmd := m.Update(eventMsg{event: core.Event{Kind: core.EventMessage, Message: &protocol.StoredMessage{
		ID: "m6", ServerSeq: 6, SenderUserID: "bob", Body: "yo", CreatedAt: 1700000000001,
	}}})
	if cmd == nil {
		t.Fatal("eventMsg 应至少返回 listenCmd 续链")
	}
}

// runCmdSafe 执行 Update 返回的 Cmd：v2 的 tea.Batch 返回 BatchMsg 而非
// 可执行函数，需拆开子命令逐个执行；listenCmd 阻塞在 inbox 上，放
// goroutine 里不影响断言（测试进程退出即回收）。
func runCmdSafe(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	// 注意：cmd() 只能执行一次——类型判断与执行必须共用同一次返回值，
	// 否则非 batch 的 Cmd（如 readCmd）副作用会重复。
	out := cmd()
	if b, ok := out.(tea.BatchMsg); ok {
		for _, c := range b {
			go c()
		}
	}
}

// waitRead 等待 fakeReader 收到指定 seq 的 SendRead（异步 Cmd 轮询）。
func waitRead(t *testing.T, f *fakeReader, want uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := f.seqs(); len(got) == 1 && got[0] == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("SendRead seq = %v，want [%d]", f.seqs(), want)
}
