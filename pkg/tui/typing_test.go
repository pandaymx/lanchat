package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakeTyper 同时实现 Sender 与 Typer（HistoryFetcher 不需要），
// 记录 SendTyping 调用次数供节流断言。
type fakeTyper struct {
	typingCalls int
}

func (f *fakeTyper) Send(context.Context, string) error { return nil }

func (f *fakeTyper) SendTyping(context.Context) error {
	f.typingCalls++
	return nil
}

// TestTyping_IncomingShowsIndicator 验证对端 typing 事件反映到 hints 行，
// 且消息到达 / typingExpireMsg（TTL 过期）后撤掉。
func TestTyping_IncomingShowsIndicator(t *testing.T) {
	m := New(Config{})
	setSize(m)

	m.applyEvent(core.Event{
		Kind:   core.EventTyping,
		Typing: &protocol.Typing{UserID: "bob", DeviceID: "dev-bob"},
	})
	hints := m.renderHints()
	if !strings.Contains(hints, "bob") || !strings.Contains(hints, "typing") {
		t.Fatalf("typing 指示应包含 bob + typing，实际 %q", hints)
	}

	// 两人 → many 模板。
	m.upsertTyping(protocol.Typing{UserID: "carol", DeviceID: "dev-carol"})
	if !strings.Contains(m.renderHints(), "are typing") {
		t.Fatalf("两人 typing 应走 many 模板，实际 %q", m.renderHints())
	}

	// 对端消息到达 → 该设备 typing 立即撤掉，只剩 carol。
	msg := protocol.StoredMessage{ID: "m1", SenderUserID: "bob", SenderDeviceID: "dev-bob", ServerSeq: 1}
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	hints = m.renderHints()
	if strings.Contains(hints, "bob") {
		t.Fatalf("bob 消息到达后不应再显示 bob typing，实际 %q", hints)
	}
	if !strings.Contains(hints, "carol") {
		t.Fatalf("carol 仍在输入，实际 %q", hints)
	}

	// TTL 过期：把时间戳拨老后触发过期检查 Tick。
	m.typings["dev-carol"] = typingState{user: "carol", at: time.Now().Add(-typingTTL - time.Second)}
	if _, cmd := m.Update(typingExpireMsg{}); cmd != nil {
		t.Fatal("最后一条过期后不应再 schedule Tick")
	}
	if got := m.renderHints(); !strings.Contains(got, "Enter") {
		t.Fatalf("typing 全部过期后 hints 应恢复键位提示，实际 %q", got)
	}
}

// TestTyping_OfflineClearsIndicator 验证设备离线撤掉其 typing 指示。
func TestTyping_OfflineClearsIndicator(t *testing.T) {
	m := New(Config{})
	setSize(m)
	m.upsertTyping(protocol.Typing{UserID: "bob", DeviceID: "dev-bob"})
	m.upsertPresence(protocol.Presence{UserID: "bob", DeviceID: "dev-bob", Online: false})
	if len(m.typings) != 0 {
		t.Fatalf("设备离线后 typing 应清空，实际 %d 条", len(m.typings))
	}
}

// TestTyping_OutgoingThrottled 验证本端输入时上发 typing 且 3s 节流。
func TestTyping_OutgoingThrottled(t *testing.T) {
	f := &fakeTyper{}
	m := New(Config{Sender: f})
	setSize(m)

	// 真实按键路径：Update 内部调 maybeNotifyTyping，返回的 batch 里
	// 含 typingCmd（与 focus/blink cmd 混在一起）；执行后轮询调用计数。
	_, cmd := m.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	if m.lastTypingAt.IsZero() {
		t.Fatal("输入按键后应已触发 typing 节流位点")
	}
	if cmd == nil {
		t.Fatal("首次输入应返回含 typingCmd 的 batch")
	}
	cmd()
	waitForTypingCalls(t, f, 1)

	// 节流窗口内再触发：no-op。
	if c := m.maybeNotifyTyping(); c != nil {
		t.Fatal("节流窗口内不应再返回 typingCmd")
	}
	waitForTypingCalls(t, f, 1)

	// 模拟节流窗口已过。
	m.lastTypingAt = time.Time{}
	if c := m.maybeNotifyTyping(); c == nil {
		t.Fatal("窗口过后应再次返回 typingCmd")
	} else {
		c()
	}
	waitForTypingCalls(t, f, 2)

	// 输入框清空后（消息已提交）不再上发。
	m.input.Reset()
	m.lastTypingAt = time.Time{}
	if c := m.maybeNotifyTyping(); c != nil {
		t.Fatal("空输入框不应返回 typingCmd")
	}
	waitForTypingCalls(t, f, 2)
}

// waitForTypingCalls 轮询 fakeTyper 的调用计数（batch cmd 在 goroutine
// 里执行，typingCmd 落计数有调度延迟）。
func waitForTypingCalls(t *testing.T, f *fakeTyper, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f.typingCalls == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if f.typingCalls != want {
		t.Fatalf("typing 调用次数 = %d，want %d", f.typingCalls, want)
	}
}

// TestTyping_NoTyperNoop 验证 sender 不实现 Typer 时输入不报错也不发帧。
func TestTyping_NoTyperNoop(t *testing.T) {
	// nil sender：类型断言失败，maybeNotifyTyping 返回 nil。
	m := New(Config{})
	setSize(m)
	_, cmd := m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	if cmd != nil {
		t.Fatalf("无 Typer 时不应返回 cmd，实际 %T", cmd)
	}
}
