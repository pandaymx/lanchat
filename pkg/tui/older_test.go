package tui

import (
	"context"
	"errors"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakeHistoryFetcher 在 fakeSender 之外加 FetchHistory 能力（M7.1）。
type fakeHistoryFetcher struct {
	fakeSender

	mu         sync.Mutex
	calls      int
	lastBefore uint64
	resp       []protocol.StoredMessage
	hasMore    bool
	err        error
}

func (f *fakeHistoryFetcher) FetchHistory(_ context.Context, before uint64, _ int) ([]protocol.StoredMessage, bool, error) {
	f.mu.Lock()
	f.calls++
	f.lastBefore = before
	f.mu.Unlock()
	return f.resp, f.hasMore, f.err
}

// pumpSeqMessages 灌入 n 条 seq 从 startSeq 起、ID 唯一的消息。
func pumpSeqMessages(m *Model, startSeq, n int) {
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < n; i++ {
		seq := uint64(startSeq + i)
		msg := protocol.StoredMessage{
			ID:             "m" + itoa(int(seq)),
			SenderUserID:   "u1",
			Body:           "line",
			ServerSeq:      seq,
			ConversationID: "lobby",
		}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
}

// TestFetchOlder_OnPgUpAtTop 验证上翻到顶时通过 HistoryFetcher 拉更早
// 历史，响应消息合并到头部、重复 ID 去重、HasMore=false 后不再触发。
func TestFetchOlder_OnPgUpAtTop(t *testing.T) {
	fetcher := &fakeHistoryFetcher{
		resp: []protocol.StoredMessage{
			{ID: "old-a", ConversationID: "lobby", SenderUserID: "u2", Body: "older-a", ServerSeq: 9},
			{ID: "old-b", ConversationID: "lobby", SenderUserID: "u2", Body: "older-b", ServerSeq: 10},
			{ID: "m11", ConversationID: "lobby", SenderUserID: "u1", Body: "dup", ServerSeq: 11}, // 与首屏重复
		},
		hasMore: false,
	}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h", Sender: fetcher})

	// 首屏 20 条（seq 11..30，模拟 catch-up 只拉到最近窗口）。
	pumpSeqMessages(m, 11, 20)

	// 一路 PgUp 到顶。
	for i := 0; i < 30 && !m.history.AtTop(); i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	if !m.history.AtTop() {
		t.Fatal("setup: 应已翻到顶部")
	}
	if fetcher.calls != 0 {
		t.Fatalf("到顶前不应触发 fetch，calls=%d", fetcher.calls)
	}

	// 再按一次 PgUp（已在顶）→ Update 返回 fetchOlderCmd；闭包执行时才真正发请求。
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if cmd == nil {
		t.Fatal("AtTop 时 PgUp 应返回 fetchOlderCmd")
	}
	if !m.loadingOlder {
		t.Fatal("发起后 loadingOlder 应为 true")
	}

	// 执行 Cmd 拿响应 Msg 并交回 Update。
	msg := cmd()
	om, ok := msg.(olderMessagesMsg)
	if !ok {
		t.Fatalf("cmd 返回类型 %T，want olderMessagesMsg", msg)
	}
	if om.err != nil {
		t.Fatalf("fetch err: %v", om.err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("FetchHistory 应被调用 1 次，实际 %d", fetcher.calls)
	}
	if fetcher.lastBefore != 11 {
		t.Fatalf("before 应为首屏最早 seq 11，实际 %d", fetcher.lastBefore)
	}
	_, _ = m.Update(om)

	if m.loadingOlder {
		t.Fatal("响应处理后 loadingOlder 应复位")
	}
	if m.olderHasMore {
		t.Fatal("hasMore=false 应被记录")
	}
	msgs := m.Messages()
	if len(msgs) != 22 {
		t.Fatalf("20 首屏 + 2 新（1 条重复去重）= 22，实际 %d", len(msgs))
	}
	if msgs[0].ID != "old-a" || msgs[1].ID != "old-b" {
		t.Fatalf("更早消息应在头部，实际前两条 %s %s", msgs[0].ID, msgs[1].ID)
	}
	if msgs[0].ServerSeq != 9 || msgs[1].ServerSeq != 10 {
		t.Fatalf("头部消息 seq 应为 9,10，实际 %d,%d", msgs[0].ServerSeq, msgs[1].ServerSeq)
	}

	// HasMore=false 后再翻顶不再请求。
	_, cmd2 := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if cmd2 != nil {
		t.Fatal("olderHasMore=false 后不应再返回 cmd")
	}
	if fetcher.calls != 1 {
		t.Fatalf("到头后不应再调用 FetchHistory，calls=%d", fetcher.calls)
	}
}

// TestFetchOlder_NoFetcherNoop 验证 sender 不实现 HistoryFetcher 时静默禁用。
func TestFetchOlder_NoFetcherNoop(t *testing.T) {
	m := New(Config{Sender: &fakeSender{}})
	pumpSeqMessages(m, 1, 20)
	for i := 0; i < 30 && !m.history.AtTop(); i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if cmd != nil {
		t.Fatal("无 HistoryFetcher 能力时不应返回 cmd")
	}
}

// TestFetchOlder_ErrorResetsLoading 验证请求失败后 loading 复位、错误可见、
// 下次到顶可重试（不把 olderHasMore 误置 false）。
func TestFetchOlder_ErrorResetsLoading(t *testing.T) {
	fetchErr := errors.New("boom")
	fetcher := &fakeHistoryFetcher{err: fetchErr, hasMore: true}
	m := New(Config{Sender: fetcher})
	pumpSeqMessages(m, 1, 20)
	for i := 0; i < 30 && !m.history.AtTop(); i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if cmd == nil {
		t.Fatal("AtTop 时应触发 fetch")
	}
	_, _ = m.Update(cmd())

	if m.loadingOlder {
		t.Fatal("失败后 loadingOlder 应复位以允许重试")
	}
	if !m.olderHasMore {
		t.Fatal("失败不应把 olderHasMore 置 false")
	}
	if m.LastError() == nil {
		t.Fatal("失败应写入 lastError 在 status 行展示")
	}
}
