package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// ---- M12-A 群聊测试 ----

// fakeConvMgr 记录会话管理调用，返回可配置结果。
type fakeConvMgr struct {
	convID  string
	convs   []protocol.ConversationSnapshot
	created []string // 建群请求（title:members 拼接）
	invited []string
	left    []string
	err     error
}

func (f *fakeConvMgr) SetConversation(convID string)                  { f.convID = convID }
func (f *fakeConvMgr) Conversations() []protocol.ConversationSnapshot { return f.convs }
func (f *fakeConvMgr) CreateConversation(ctx context.Context, title string, memberIDs []string) (protocol.Conversation, error) {
	f.created = append(f.created, title+":"+strings.Join(memberIDs, ","))
	return protocol.Conversation{Title: title}, f.err
}

func (f *fakeConvMgr) InviteToConversation(ctx context.Context, convID string, userIDs []string) error {
	f.invited = append(f.invited, convID+":"+strings.Join(userIDs, ","))
	return f.err
}

func (f *fakeConvMgr) LeaveConversation(ctx context.Context, convID string) error {
	f.left = append(f.left, convID)
	return f.err
}

// TestConvMatchTUI 验证大厅两种写法的等价性与精确匹配。
func TestConvMatchTUI(t *testing.T) {
	cases := []struct {
		want, got string
		match     bool
	}{
		{"", "", true},
		{"", "lobby", true},
		{"lobby", "", true},
		{"lobby", "lobby", true},
		{"", "c1", false},
		{"c1", "", false},
		{"c1", "c1", true},
		{"c1", "c2", false},
	}
	for _, tc := range cases {
		if got := convMatchTUI(tc.want, tc.got); got != tc.match {
			t.Errorf("convMatchTUI(%q,%q)=%v want %v", tc.want, tc.got, got, tc.match)
		}
	}
}

// TestModel_MessageFilteredByConv 验证其它会话的消息不进视图、
// 提示当前会话（convNotice 记录标题）。
func TestModel_MessageFilteredByConv(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{
		convID: "",
		convs: []protocol.ConversationSnapshot{
			{Conversation: protocol.Conversation{ID: "c1", Title: "Go"}, Members: []string{"alice", "bob"}},
		},
	}
	m.AttachConversations(mgr)
	m.Update(teaWindow(120, 40))
	// FKConvList 握手事件填充 convs（真实流程必有）。
	m.Update(eventMsg{event: core.Event{Kind: core.EventConversation}})

	// 群 c1 的消息 → 过滤 + 提示
	ev := eventMsg{event: core.Event{
		Kind:           core.EventMessage,
		ConversationID: "c1",
		Message: &protocol.StoredMessage{
			ID: "m1", ServerSeq: 1, ConversationID: "c1",
			SenderUserID: "bob", Body: "hi in group",
		},
	}}
	if _, cmd := m.Update(ev); cmd == nil {
		t.Fatal("Update(eventMsg) should return listenCmd")
	}
	if len(m.messages) != 0 {
		t.Fatalf("foreign-conv message must not enter view, got %d messages", len(m.messages))
	}
	if !strings.Contains(m.convNotice, "Go") {
		t.Errorf("convNotice = %q, want mention of group title", m.convNotice)
	}
}

// TestModel_CurrentConvMessageAppends 验证当前会话消息正常进入视图。
func TestModel_CurrentConvMessageAppends(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{convID: ""}
	m.AttachConversations(mgr)

	ev := eventMsg{event: core.Event{
		Kind:           core.EventMessage,
		ConversationID: "",
		Message: &protocol.StoredMessage{
			ID: "m1", ServerSeq: 1, ConversationID: "",
			SenderUserID: "bob", Body: "hello lobby",
		},
	}}
	m.Update(ev)
	if len(m.messages) != 1 || m.messages[0].Body != "hello lobby" {
		t.Fatalf("lobby message should append, got %+v", m.messages)
	}
}

// TestModel_RoomsCommand 验证 /rooms 生成会话列表并标当前会话。
func TestModel_RoomsCommand(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{
		convID: "",
		convs: []protocol.ConversationSnapshot{
			{Conversation: protocol.Conversation{ID: "c1", Title: "Go"}, Members: []string{"a", "b"}},
		},
	}
	m.AttachConversations(mgr)
	m.Update(eventMsg{event: core.Event{Kind: core.EventConversation}})
	m.convID = "c1"
	m.convMgr.SetConversation("c1")

	// /rooms 返回 listenCmd（读 inbox）；渲染结果已同步写入 convNotice。
	_ = m.tryCommand("/rooms")
	if !strings.Contains(m.convNotice, "c1") || !strings.Contains(m.convNotice, "Go") {
		t.Errorf("rooms output = %q, want conv id+title", m.convNotice)
	}
	if !strings.Contains(m.convNotice, "*") {
		t.Errorf("rooms output should mark current conv with *, got %q", m.convNotice)
	}
}

// TestModel_JoinCommand 验证 /join 切换会话：SetConversation + 清消息 +
// 触发首屏历史拉取。
func TestModel_JoinCommand(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{convID: ""}
	m.AttachConversations(mgr)

	cmd := m.tryCommand("/join c1")
	if cmd != nil {
		_ = cmd() // 无 HistoryFetcher 时 switchConv 返回 nil；有则拉首屏
	}
	if m.convID != "c1" || mgr.convID != "c1" {
		t.Errorf("conv not switched: model=%q mgr=%q", m.convID, mgr.convID)
	}
}

// TestModel_GroupCommand 验证 /group 异步建群：成功后置 pendingJoin。
func TestModel_GroupCommand(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{convID: ""}
	m.AttachConversations(mgr)

	cmd := m.tryCommand("/group Go alice bob")
	if cmd == nil {
		t.Fatal("/group should return a cmd")
	}
	msg := cmd()
	if _, ok := msg.(sentMsg); !ok {
		t.Fatalf("/group result = %T, want sentMsg", msg)
	}
	if len(mgr.created) != 1 || mgr.created[0] != "Go:alice,bob" {
		t.Errorf("CreateConversation calls = %v, want [Go:alice,bob]", mgr.created)
	}
	if m.pendingJoin != "Go" {
		t.Errorf("pendingJoin = %q, want Go", m.pendingJoin)
	}
}

// TestModel_GroupCommand_Error 验证建群失败走错误提示、不置 pendingJoin。
func TestModel_GroupCommand_Error(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{convID: "", err: errors.New("boom")}
	m.AttachConversations(mgr)

	cmd := m.tryCommand("/group Go")
	msg := cmd()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("failed create result = %T, want errMsg", msg)
	}
	if m.pendingJoin != "" {
		t.Errorf("pendingJoin should stay empty on error, got %q", m.pendingJoin)
	}
}

// TestModel_ConversationEventUpdatesSnapshot 验证 FKConvList 快照事件
// 更新 Model.convs（convMgr.Conversations 被重新拉取）。
func TestModel_ConversationEventUpdatesSnapshot(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{
		convs: []protocol.ConversationSnapshot{
			{Conversation: protocol.Conversation{ID: "c1", Title: "Go"}, Members: []string{"alice"}},
		},
	}
	m.AttachConversations(mgr)

	m.Update(eventMsg{event: core.Event{Kind: core.EventConversation}})
	if len(m.convs) != 1 || m.convs[0].Conversation.ID != "c1" {
		t.Fatalf("convs not refreshed after EventConversation: %+v", m.convs)
	}
}

// TestModel_ConversationEventPendingJoin 验证建群后 FKConvEvent created
// 事件命中 pendingJoin 自动跳转新群。
func TestModel_ConversationEventPendingJoin(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{
		convID: "",
		convs: []protocol.ConversationSnapshot{
			{Conversation: protocol.Conversation{ID: "g9", Title: "Go"}, Members: []string{"alice"}},
		},
	}
	m.AttachConversations(mgr)
	m.pendingJoin = "Go"

	cmd := m.applyEvent(core.Event{
		Kind: core.EventConversation,
		Conversation: &protocol.ConversationSnapshot{
			Conversation: protocol.Conversation{ID: "g9", Title: "Go"},
			Members:      []string{"alice"},
		},
	})
	if cmd != nil {
		_ = cmd()
	}
	if m.convID != "g9" || m.pendingJoin != "" {
		t.Errorf("after join: convID=%q pendingJoin=%q", m.convID, m.pendingJoin)
	}
}

// TestModel_LeaveCommand 验证 /leave 退当前群并回大厅。
func TestModel_LeaveCommand(t *testing.T) {
	m := New(Config{})
	mgr := &fakeConvMgr{convID: "c1"}
	m.AttachConversations(mgr)
	m.convID = "c1"

	cmd := m.tryCommand("/leave")
	if cmd == nil {
		t.Fatal("/leave should return a cmd")
	}
	_ = cmd()
	if len(mgr.left) != 1 || mgr.left[0] != "c1" {
		t.Errorf("LeaveConversation calls = %v, want [c1]", mgr.left)
	}
	if m.convID != "" || mgr.convID != "" {
		t.Errorf("should return to lobby: model=%q mgr=%q", m.convID, mgr.convID)
	}
}

// teaWindow 构造一个 WindowSizeMsg（测试内联小工具）。
func teaWindow(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }
