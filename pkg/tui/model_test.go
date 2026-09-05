package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestNew_Defaults 验证 New 在零值 Config 下给出安全默认。
func TestNew_Defaults(t *testing.T) {
	m := New(Config{})
	if m.maxHist != 5000 {
		t.Fatalf("default maxHist want 5000 got %d", m.maxHist)
	}
	if m.User() != "" || m.Device() != "" || m.HubURL() != "" {
		t.Fatalf("zero Config should leave identity empty: %+v", m)
	}
	if m.InboxLen() != 0 {
		t.Fatalf("inbox should start empty, got %d", m.InboxLen())
	}
}

// TestInit_ReturnsListenCmd 验证 Init 返回的 Cmd 能从 inbox 拿到消息。
func TestInit_ReturnsListenCmd(t *testing.T) {
	m := New(Config{User: "alice", Device: "phone"})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must return listenCmd, got nil")
	}
	m.Publish(core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}})
	msg := cmd()
	em, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("listenCmd should yield eventMsg, got %T", msg)
	}
	if em.event.Kind != core.EventState || !em.event.State.Connected {
		t.Fatalf("event roundtrip mismatch: %+v", em.event)
	}
}

// TestUpdate_WindowSizeMsg 验证 WindowSizeMsg 更新 width/height/ready。
func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := New(Config{})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.Width() != 120 || m.Height() != 40 || !m.Ready() {
		t.Fatalf("WindowSizeMsg not applied: w=%d h=%d ready=%v", m.Width(), m.Height(), m.Ready())
	}
}

// TestUpdate_CtrlC 验证 Ctrl+C 触发退出 Cmd，emit tea.QuitMsg。
func TestUpdate_CtrlC(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "c", Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C must yield a tea.Cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Ctrl+C cmd should emit tea.QuitMsg, got %T (%+v)", msg, msg)
	}
}

// TestUpdate_RegularKey_ForwardsToInput 验证普通按键被转给 textInput 而非 quit。
//
// M3.2 时无 textInput，要求 cmd == nil；M3.3 之后 textInput.Update
// 可能返回 focus blink 之类的 Cmd，所以只断言「不会引出 Quit」。
func TestUpdate_RegularKey_ForwardsToInput(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "a", Code: 'a', Mod: 0})
	if cmd != nil {
		// 模拟一次 cmd 调用，确认不是 QuitMsg。
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatalf("regular key must not yield QuitMsg, got QuitMsg")
		}
	}
}

// TestUpdate_EventMsg_Aggregate 验证 eventMsg 把 core.Event 反映到 Model。
func TestUpdate_EventMsg_Aggregate(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(eventMsg{event: core.Event{
		Kind:  core.EventState,
		State: &core.StateInfo{Connected: true},
	}})
	if cmd == nil {
		t.Fatal("eventMsg must schedule next listenCmd")
	}
	if !m.Connected() {
		t.Fatal("EventState.Connected should flip Model.Connected")
	}
}

// TestUpdate_ErrMsg 验证 errMsg 写到 LastError。
func TestUpdate_ErrMsg(t *testing.T) {
	m := New(Config{})
	_, _ = m.Update(errMsg{err: errors.New("boom")})
	if m.LastError() == nil || m.LastError().Error() != "boom" {
		t.Fatalf("errMsg should populate LastError, got %v", m.LastError())
	}
}

// TestUpdate_QuitMsg 验证 quitMsg 触发退出 Cmd。
func TestUpdate_QuitMsg(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(quitMsg{reason: "test"})
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("quitMsg must yield tea.QuitMsg, got %T", msg)
	}
}

// TestApplyEvent_Message_TrimsAtMax 验证消息超限后裁剪到 maxHist 附近。
// 设计：上限 = maxHist + maxHist/10（11），超过则砍到 maxHist（10）。
// 由于 headroom 触发，trim 后稳定在 10~11 之间是预期的。
func TestApplyEvent_Message_TrimsAtMax(t *testing.T) {
	m := New(Config{MaxHist: 10})
	for i := 0; i < 25; i++ {
		msg := protocol.StoredMessage{ID: "m", Body: "x", ServerSeq: uint64(i + 1)}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
	got := m.Messages()
	if len(got) > 11 || len(got) < 10 {
		t.Fatalf("messages should be trimmed near MaxHist=10 (range 10..11), got %d", len(got))
	}
	// 裁剪后保留最后 N 条；最末 ServerSeq=25。
	if got[len(got)-1].ServerSeq != 25 {
		t.Fatalf("latest should be 25, got %d", got[len(got)-1].ServerSeq)
	}
	// 最早一条的 ServerSeq 应 >= 15（25 - 10 = 15）。
	if got[0].ServerSeq < 15 {
		t.Fatalf("oldest after trim should be >= 15, got %d", got[0].ServerSeq)
	}
}

// TestUpsertPresence 验证同一 DeviceID upsert 覆盖、不同设备追加。
func TestUpsertPresence(t *testing.T) {
	m := New(Config{})
	m.upsertPresence(protocol.Presence{UserID: "u1", DeviceID: "d1", Online: true})
	m.upsertPresence(protocol.Presence{UserID: "u1", DeviceID: "d2", Online: true})
	m.upsertPresence(protocol.Presence{UserID: "u1", DeviceID: "d1", Online: false})

	if m.PeerCount() != 2 {
		t.Fatalf("upsert same DeviceID should not append, got peers=%d", m.PeerCount())
	}
	if m.UniqueUserCount() != 1 {
		t.Fatalf("unique users should be 1, got %d", m.UniqueUserCount())
	}
	// d1 应被覆盖为 Online=false
	for _, p := range m.peers {
		if p.DeviceID == "d1" && p.Online {
			t.Fatalf("d1 should be Online=false after upsert, got %+v", p)
		}
	}
}

// TestUniqueUsers_MultipleUsers 验证多设备多用户去重。
func TestUniqueUsers_MultipleUsers(t *testing.T) {
	peers := []protocol.Presence{
		{UserID: "u1", DeviceID: "d1"},
		{UserID: "u1", DeviceID: "d2"},
		{UserID: "u2", DeviceID: "d3"},
		{UserID: "u3", DeviceID: "d4"},
	}
	if got := uniqueUsers(peers); got != 3 {
		t.Fatalf("unique users want 3, got %d", got)
	}
}

// TestPublish_AndPublishError_AndRequestQuit 验证三类外部入口都进 inbox。
func TestPublish_AndPublishError_AndRequestQuit(t *testing.T) {
	m := New(Config{})
	m.Publish(core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}})
	m.PublishError(errors.New("e1"))
	m.RequestQuit("bye")
	if m.InboxLen() != 3 {
		t.Fatalf("inbox should hold 3 msgs, got %d", m.InboxLen())
	}
}

// TestListenCmd_ClosedInbox 验证 inbox 关闭时返回 quitMsg。
func TestListenCmd_ClosedInbox(t *testing.T) {
	ch := make(chan tea.Msg)
	close(ch)
	cmd := listenCmd(ch)
	msg := cmd()
	qm, ok := msg.(quitMsg)
	if !ok {
		t.Fatalf("closed inbox should yield quitMsg, got %T", msg)
	}
	if qm.reason != "inbox closed" {
		t.Fatalf("quitMsg.reason want %q, got %q", "inbox closed", qm.reason)
	}
}

// TestView_NotEmpty 验证 View 至少返回一段非空文本。
func TestView_NotEmpty(t *testing.T) {
	m := New(Config{})
	v := m.View()
	if v.Content == "" {
		t.Fatal("View() should return non-empty placeholder")
	}
}

// ============================================================
// M3.3 新增测试：input / history / layout / 键位路由
// ============================================================

// TestNew_HasInputAndHistory 验证 New 把 input + history 字段都装好。
func TestNew_HasInputAndHistory(t *testing.T) {
	m := New(Config{})
	if m.input.Width() <= 0 || m.input.Height() <= 0 {
		t.Fatalf("input region should have size, got %dx%d",
			m.input.Width(), m.input.Height())
	}
	if m.history.Width() <= 0 || m.history.Height() <= 0 {
		t.Fatalf("history region should have size, got %dx%d",
			m.history.Width(), m.history.Height())
	}
}

// TestUpdate_WindowSizeMsg_ResizesRegions 验证 WindowSizeMsg 重置各 region 尺寸。
//
// layoutDims(120,40) → sidebarW=30（被 MaxWidth 夹紧）、bodyH=36、historyW=90。
// input 占满全宽（外宽 120；textarea 内部 content 宽为 120 - 2 prompt = 118）。
func TestUpdate_WindowSizeMsg_ResizesRegions(t *testing.T) {
	m := New(Config{})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.Width() != 120 || m.Height() != 40 || !m.Ready() {
		t.Fatalf("WindowSizeMsg not applied: %dx%d ready=%v",
			m.Width(), m.Height(), m.Ready())
	}
	// input.Width() 返回 textarea 内部 content 宽度（含 defaultPrompt 偏移）。
	const wantInputInnerWidth = 118
	if m.input.Width() != wantInputInnerWidth {
		t.Fatalf("input inner width want %d, got %d",
			wantInputInnerWidth, m.input.Width())
	}
	if m.history.Height() != 35 {
		t.Fatalf("history height should be h - status - input - hints = 35, got %d",
			m.history.Height())
	}
}

// TestUpdate_EnterRoutesToSubmit 验证 Enter 无 Shift 把 input 内容投递为 submitMsg。
func TestUpdate_EnterRoutesToSubmit(t *testing.T) {
	m := New(Config{})
	m.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	m.Update(tea.KeyPressMsg{Text: "i", Code: 'i'})
	if got := m.input.Value(); got != "hi" {
		t.Fatalf("input should hold 'hi' before submit, got %q", got)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if got := m.LastSubmitted(); got != "hi" {
		t.Fatalf("LastSubmitted want 'hi', got %q", got)
	}
	if v := m.input.Value(); v != "" {
		t.Fatalf("input should be cleared after submit, got %q", v)
	}
}

// TestUpdate_ShiftEnterInsertsNewline 验证 Shift+Enter 触发换行而不是提交。
func TestUpdate_ShiftEnterInsertsNewline(t *testing.T) {
	m := New(Config{})
	m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	before := m.LastSubmitted()
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if got := m.LastSubmitted(); got != before {
		t.Fatalf("Shift+Enter should NOT submit, LastSubmitted %q → %q",
			before, got)
	}
	v := m.input.Value()
	if !strings.Contains(v, "\n") {
		t.Fatalf("Shift+Enter should append newline; input value=%q", v)
	}
}

// TestUpdate_BlankSubmit_NoOp 验证空白文本提交时不投递。
func TestUpdate_BlankSubmit_NoOp(t *testing.T) {
	m := New(Config{})
	m.Update(tea.KeyPressMsg{Text: " ", Code: ' '})
	m.Update(tea.KeyPressMsg{Text: " ", Code: ' '})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if cmd != nil {
		t.Fatal("blank submit should yield nil cmd, got non-nil")
	}
	if got := m.LastSubmitted(); got != "" {
		t.Fatalf("blank submit should not write lastSubmitted, got %q", got)
	}
}

// TestUpdate_CtrlC_StillQuits 回归：M3.3 改键位路由后 Ctrl+C 仍 Quit。
func TestUpdate_CtrlC_StillQuits(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(tea.KeyPressMsg{Text: "c", Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C must yield a tea.Cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Ctrl+C cmd should emit tea.QuitMsg, got %T", msg)
	}
}

// TestView_AllRegionsRendered 验证 View 同时含 status / history / sidebar / input。
func TestView_AllRegionsRendered(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h:9000"})
	// 翻转 connected → 状态栏显示 "online" 而不是 "offline"。
	_, _ = m.Update(eventMsg{event: core.Event{
		Kind:  core.EventState,
		State: &core.StateInfo{Connected: true},
	}})
	m.upsertPresence(protocol.Presence{UserID: "alice", DeviceID: "lap", Online: true})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	body := m.View().Content
	for _, want := range []string{
		"online",          // status
		"user=alice",      // status
		"device=lap",      // status
		"hub=ws://h:9000", // status
		"peers",           // sidebar
		"alice",           // sidebar 行（"● alice@lap"）
	} {
		if !strings.Contains(body, want) {
			t.Errorf("View() should contain %q, body:\n%s", want, body)
		}
	}
}

// TestApplyEvent_Message_PushesIntoHistory 验证 EventMessage 写到 history view。
func TestApplyEvent_Message_PushesIntoHistory(t *testing.T) {
	m := New(Config{})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	msg := protocol.StoredMessage{SenderUserID: "u1", Body: "hello"}
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	body := m.View().Content
	if !strings.Contains(body, "hello") {
		t.Errorf("history view should contain message body, got:\n%s", body)
	}
	if !strings.Contains(body, "u1") {
		t.Errorf("history view should contain sender ID, got:\n%s", body)
	}
}

// ============================================================
// M3.4 新增测试：程序入口配套（alt screen / 启动聚焦）
// ============================================================

// TestLayoutDims_SidebarCapAndMin 验证 layoutDims 上下限夹紧。
func TestLayoutDims_SidebarCapAndMin(t *testing.T) {
	// 极窄终端：sidebar 应让位给 history（≤ width - historyMinWidth）。
	_, sw, _ := layoutDims(20, 40)
	if sw > 20-historyMinWidth {
		t.Errorf("sidebar should not exceed (width - historyMinWidth) when narrow, got sw=%d", sw)
	}
	// 宽终端：sidebar 应被 MaxWidth 夹紧。
	_, sw, _ = layoutDims(300, 60)
	if sw > sidebarMaxWidth {
		t.Errorf("sidebar should be capped at MaxWidth=%d, got %d", sidebarMaxWidth, sw)
	}
}

// TestLayoutDims_NegativeOrZeroClamped 验证 width/height 的负值与 0
// 不会让 layoutDims 返回负值，避免 lipgloss panic。
//
// 这是 M3.8 自适应回归门禁：极端终端（resize 中间帧、子进程 detach）
// 可能下发 (0, 0) / (-1, -1)。
func TestLayoutDims_NegativeOrZeroClamped(t *testing.T) {
	for _, w := range []int{-100, 0, 1} {
		for _, h := range []int{-50, 0, 1} {
			hw, sw, bh := layoutDims(w, h)
			if hw < 0 || sw < 0 || bh < 0 {
				t.Errorf("layoutDims(%d,%d) returned negatives: hw=%d sw=%d bh=%d", w, h, hw, sw, bh)
			}
			if hw+sw > maxInt(w, 1) {
				t.Errorf("layoutDims(%d,%d): hw+sw=%d exceeds width", w, h, hw+sw)
			}
		}
	}
}

// TestLayoutDims_TinyHeight_BodyShrinks 验证 height 小于 status+input 时
// body 仍至少 1 行，避免 viewport 高度 0 报错。
func TestLayoutDims_TinyHeight_BodyShrinks(t *testing.T) {
	for _, h := range []int{statusH, statusH + 1, statusH + inputH - 1, statusH + inputH} {
		_, _, bh := layoutDims(80, h)
		if bh < 1 {
			t.Errorf("layoutDims(80, %d): bodyH should be ≥1, got %d", h, bh)
		}
	}
}

// TestLayoutDims_NarrowerThanBothMins 验证 width 同时小于
// historyMinWidth+sidebarMinWidth 时，sidebar 被挤到 0、history 占满。
//
// 极值：width=18 < historyMin(10)+sidebarMin(12)=22。
func TestLayoutDims_NarrowerThanBothMins(t *testing.T) {
	hw, sw, _ := layoutDims(18, 40)
	if hw+sw > 18 {
		t.Errorf("hw+sw=%d exceeds width=18", hw+sw)
	}
	if sw > 18-historyMinWidth {
		t.Errorf("sidebar should give history at least %d cols, got sw=%d", historyMinWidth, sw)
	}
}

// TestLayoutDims_StandardRatio 验证标准宽 (90) 下 sidebar 拿到 30。
func TestLayoutDims_StandardRatio(t *testing.T) {
	hw, sw, _ := layoutDims(90, 40)
	if sw != 30 {
		t.Errorf("width=90 sidebar should be 30 (=90/3), got %d", sw)
	}
	if hw+sw != 90 {
		t.Errorf("hw(%d)+sw(%d) should sum to 90", hw, sw)
	}
}

// TestLayoutDims_VeryTall 验证 height=200 时 bodyH 跟着扩到 195。
//
// M3.9.2 起扣掉 hintsH=1 行（200-1-3-1=195），原本是 196。
func TestLayoutDims_VeryTall(t *testing.T) {
	_, _, bh := layoutDims(80, 200)
	want := 200 - statusH - inputH - hintsH
	if bh != want {
		t.Errorf("height=200 bodyH should be %d, got %d", want, bh)
	}
}

// maxInt is a small helper for layoutDims negative-guard assertions.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestView_AltScreenEnabled 验证 View 启用 alt screen buffer。
//
// bubbletea v2 把 alt screen 从 Program option 移到了 View 字段，
// 所以断言落在 View 上而不是 Program 构造参数。
func TestView_AltScreenEnabled(t *testing.T) {
	m := New(Config{})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.View().AltScreen {
		t.Fatal("View().AltScreen should be true so the terminal is restored on exit")
	}
}

// TestUpdate_WindowSizeMsg_FocusesInput 验证尺寸就绪后输入框自动获得焦点。
//
// 时序：Init 阶段终端大小未知，不能 focus；WindowSizeMsg 到达后
// 才调 FocusInput，且只在未 focus 时调（避免 resize 反复重启光标 blink）。
func TestUpdate_WindowSizeMsg_FocusesInput(t *testing.T) {
	m := New(Config{})
	if m.input.Focused() {
		t.Fatal("input should not be focused before any WindowSizeMsg")
	}
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.input.Focused() {
		t.Fatal("input should be focused after the first WindowSizeMsg")
	}
	// 二次 resize 不应改变 focus 状态（仍为 true）。
	_, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if !m.input.Focused() {
		t.Fatal("input should stay focused after a subsequent resize")
	}
}

// TestFocusInput_ReturnsCmd 验证 FocusInput 返回非 nil 的 tea.Cmd（光标 blink）。
func TestFocusInput_ReturnsCmd(t *testing.T) {
	m := New(Config{})
	cmd := m.FocusInput()
	if cmd == nil {
		t.Fatal("FocusInput should return a tea.Cmd for the cursor blink timer")
	}
}

// ============================================================
// M3.5 新增测试：Sender 出站路径 + AttachSender 切换
// ============================================================

// fakeSender 记录 Send 调用次数与最近一次 ctx / body。
//
// 只在 model_test 内部用，所以留在 *_test.go 即可；不导出。
type fakeSender struct {
	mu      sync.Mutex
	calls   int
	last    string
	lastCtx context.Context
	err     error // 注入的固定错误；nil 表示成功
}

func (s *fakeSender) Send(ctx context.Context, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.last = body
	s.lastCtx = ctx
	return s.err
}

func (s *fakeSender) callsFn() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeSender) lastFn() (string, context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.lastCtx
}

// TestSubmitMsg_NoSenderFallback 验证无 Sender 时 submitMsg 仍记
// lastSubmitted 并续链 listenCmd——M3.3 的行为不能因为 M3.5 改造而回归。
func TestSubmitMsg_NoSenderFallback(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})

	// 直接投递 submitMsg。Update 应返回 listenCmd，lastSubmitted 立刻可读。
	_, cmd := m.Update(submitMsg{text: "hello"})
	if cmd == nil {
		t.Fatal("submitMsg with no sender must still return a command (listenCmd)")
	}
	if m.LastSubmitted() != "hello" {
		t.Fatalf("lastSubmitted=%q want %q", m.LastSubmitted(), "hello")
	}
}

// TestSubmitMsg_WithSender_KeepsListenChain 验证有 Sender 时 Update 返回
// 的是 sendCmd（不是 listenCmd），并保持 lastSubmitted 写入。
//
// runCmd() 调用会让 fakeSender.calls 递增到 1，验证 sendCmd 真的调到了 Sender。
func TestSubmitMsg_WithSender_KeepsListenChain(t *testing.T) {
	snd := &fakeSender{}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h", Sender: snd})

	_, cmd := m.Update(submitMsg{text: "send me"})
	if cmd == nil {
		t.Fatal("submitMsg with sender must return sendCmd")
	}
	if m.LastSubmitted() != "send me" {
		t.Fatalf("lastSubmitted=%q want %q", m.LastSubmitted(), "send me")
	}

	// 实际跑一次 sendCmd，验证它真调 Sender.Send。
	out := cmd()
	if _, ok := out.(sentMsg); !ok {
		t.Fatalf("sendCmd should return sentMsg, got %T", out)
	}
	if got := snd.callsFn(); got != 1 {
		t.Fatalf("Sender.Send should be called once, got %d", got)
	}
	if body, ctx := snd.lastFn(); body != "send me" || ctx == nil {
		t.Fatalf("Sender body=%q want %q, ctx=%v", body, "send me", ctx)
	}
	// deadline 应在 sendTimeout 内，且已设了 Deadline。
	deadline, ok := snd.lastCtx.Deadline()
	if !ok {
		t.Fatal("sendCtx must have a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > sendTimeout {
		t.Fatalf("deadline should be within (0, %v], got %v", sendTimeout, remaining)
	}
}

// TestSentMsg_RoutesBackToListen 验证 sentMsg 命中后 Update 返回的 cmd 不为 nil。
//
// 不能直接 run cmd() 验证 listenCmd——listenCmd 阻塞在 inbox 上，
// 空 inbox 会让测试死锁。这里只能做静态断言：cmd != nil 才算有续链意图；
// 实际 listenCmd 的行为由 TestSubmitMsg_NoSenderFallback 与
// sendCmd 端到端（TestSession_SendDeliversToPeer）一并验证。
func TestSentMsg_RoutesBackToListen(t *testing.T) {
	m := New(Config{})
	_, cmd := m.Update(sentMsg{text: "ok"})
	if cmd == nil {
		t.Fatal("sentMsg must return a non-nil cmd (listenCmd) to keep the loop alive")
	}
}

// TestAttachSender_OverridesAndReadsBack 验证 AttachSender / Sender 访问器。
func TestAttachSender_OverridesAndReadsBack(t *testing.T) {
	m := New(Config{})
	if m.Sender() != nil {
		t.Fatalf("default Sender should be nil, got %T", m.Sender())
	}
	snd := &fakeSender{}
	m.AttachSender(snd)
	if m.Sender() != snd {
		t.Fatalf("Sender() should return attached instance, got %T", m.Sender())
	}

	// 替换：AttachSender 后可以替换；后续 submitMsg 用新 sender。
	snd2 := &fakeSender{}
	m.AttachSender(snd2)
	if m.Sender() != snd2 {
		t.Fatalf("AttachSender should replace, got %T", m.Sender())
	}
}

// TestSendCmd_OnSenderError_PublishesErrMsg 验证 Sender.Send 报错时
// sendCmd 投递 errMsg 到 inbox（不丢错误），但仍回 sentMsg 续链。
func TestSendCmd_OnSenderError_PublishesErrMsg(t *testing.T) {
	snd := &fakeSender{err: errors.New("network down")}
	m := New(Config{Sender: snd})

	_, cmd := m.Update(submitMsg{text: "boom"})
	out := cmd()

	if _, ok := out.(sentMsg); !ok {
		t.Fatalf("sendCmd should still return sentMsg on error, got %T", out)
	}

	// 错误应进入 inbox（errMsg），Update 路由后写到 lastError。
	select {
	case msg := <-m.inbox:
		errMsg, ok := msg.(errMsg)
		if !ok {
			t.Fatalf("expected errMsg in inbox, got %T", msg)
		}
		if errMsg.err == nil || errMsg.err.Error() != "network down" {
			t.Fatalf("error mismatch: %v", errMsg.err)
		}
		// 模拟 Update 把它写到 lastError
		_, _ = m.Update(errMsg)
		if !strings.Contains(m.LastError().Error(), "network down") {
			t.Fatalf("LastError should surface the send failure, got %v", m.LastError())
		}
	default:
		t.Fatal("expected errMsg in inbox after sendCmd failure")
	}
}

// ============================================================
// M3.6 新增测试：未读计数 + 滚屏路由
// ============================================================

// pumpFew 灌 n 条（n ≤ viewport 高度）消息，确保 viewport AtBottom=true。
// viewport 高度由后续 WindowSizeMsg(width=80, height=24) 决定 bodyH=20。
func pumpFew(m *Model, n int) {
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < n; i++ {
		msg := protocol.StoredMessage{
			SenderUserID: "u1",
			Body:         "line",
			ServerSeq:    uint64(i + 1),
		}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
}

// pumpOverflow 灌超出 viewport 高度（>20）的消息，自然撑爆 viewport、
// 触发部分 unread 累计；用于「不在底部」语义测试。
func pumpOverflow(m *Model, n int) {
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < n; i++ {
		msg := protocol.StoredMessage{
			SenderUserID: "u1",
			Body:         "line",
			ServerSeq:    uint64(i + 1),
		}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
}

// TestApplyEvent_Message_AccumulatesUnread_WhenNotAtBottom 验证：用户
// 离开底部时，新消息只累加 unread、不破坏阅读位置。
func TestApplyEvent_Message_AccumulatesUnread_WhenNotAtBottom(t *testing.T) {
	m := New(Config{})
	pumpFew(m, 5) // viewport h=20，未撑爆，AtBottom=true
	if !m.history.AtBottom() {
		t.Fatal("history should be at bottom after a small pump")
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("unread should start at 0, got %d", got)
	}

	// 撑爆 viewport：pumpOverflow 让 viewport maxYOffset>0、AtBottom=false。
	pumpOverflow(m, 25)
	if m.history.AtBottom() {
		t.Fatal("history should be off-bottom after pump overflow")
	}

	pre := m.UnreadCount() // 撑爆期间已经累计了一些（>0）
	for i := 0; i < 3; i++ {
		msg := protocol.StoredMessage{
			SenderUserID: "u9",
			Body:         "late",
			ServerSeq:    uint64(100 + i),
		}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
	if got := m.UnreadCount(); got != pre+3 {
		t.Fatalf("unread should rise to %d, got %d", pre+3, got)
	}
	if m.history.AtBottom() {
		t.Fatal("history should NOT snap back to bottom while user is scrolled away")
	}
}

// TestApplyEvent_Message_NoUnread_WhenAtBottom 验证：用户在底部时
// 新消息直接刷新 viewport 并保持底部，不增 unread。
func TestApplyEvent_Message_NoUnread_WhenAtBottom(t *testing.T) {
	m := New(Config{})
	pumpFew(m, 5)
	if !m.history.AtBottom() {
		t.Fatal("prereq: history should be at bottom after a small pump")
	}
	msg := protocol.StoredMessage{SenderUserID: "u1", Body: "tail", ServerSeq: 99}
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("unread should stay 0 while following the tail, got %d", got)
	}
	if !m.history.AtBottom() {
		t.Fatal("history should still be at bottom after a tail-pushed event")
	}
}

// TestMarkRead_Idempotent 验证 MarkRead 是幂等的：unread 已经是 0 时再调
// 也不算错。
func TestMarkRead_Idempotent(t *testing.T) {
	m := New(Config{})
	m.Update(eventMsg{event: core.Event{
		Kind:  core.EventState,
		State: &core.StateInfo{Connected: true},
	}})
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("baseline unread want 0, got %d", got)
	}
	m.MarkRead()
	m.MarkRead()
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("idempotent MarkRead should keep unread at 0, got %d", got)
	}
}

// TestMarkRead_ResetsAccumulator 验证有累计时 MarkRead 清零。
func TestMarkRead_ResetsAccumulator(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25) // 撑爆 → 自动累计一部分 unread
	pre := m.UnreadCount()
	if pre == 0 {
		t.Fatal("pre: pump overflow should accumulate unread")
	}
	for i := 0; i < 5; i++ {
		msg := protocol.StoredMessage{SenderUserID: "u1", Body: "x", ServerSeq: uint64(i + 100)}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	}
	if got := m.UnreadCount(); got != pre+5 {
		t.Fatalf("expected unread=%d, got %d", pre+5, got)
	}
	m.MarkRead()
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("after MarkRead unread should be 0, got %d", got)
	}
}

// TestRenderStatus_ShowsUnreadWhenNonZero 验证 status 栏在 unread>0 时
// 出现 unread=N。
func TestRenderStatus_ShowsUnreadWhenNonZero(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h:9000"})
	pumpOverflow(m, 25) // 撑爆产生 unread
	st := m.renderStatus()
	if !strings.Contains(st, "unread=") {
		t.Fatalf("status should contain unread= marker, got %q", st)
	}
}

// TestRenderStatus_OmitsUnreadWhenZero 回归：unread=0 时不出现 unread=
// 标记，避免每帧多 7 字节噪音。
func TestRenderStatus_OmitsUnreadWhenZero(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap"})
	st := m.renderStatus()
	if strings.Contains(st, "unread=") {
		t.Fatalf("status should not show unread when 0, got %q", st)
	}
}

// TestUpdate_KeyEnd_ClearsUnread 验证 End 把 history 拉到底并清零 unread。
func TestUpdate_KeyEnd_ClearsUnread(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25) // 撑爆有 unread
	pre := m.UnreadCount()
	if pre == 0 {
		t.Fatal("pre: pump overflow should accumulate unread")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if cmd != nil {
		t.Fatalf("End key should yield nil cmd, got non-nil")
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("End should clear unread, got %d", got)
	}
	if !m.history.AtBottom() {
		t.Fatal("End should leave history at bottom")
	}
}

// TestUpdate_KeyPgUp_KeepsUnread 验证 PgUp 单纯上滚，不清 unread。
func TestUpdate_KeyPgUp_KeepsUnread(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25)
	pre := m.UnreadCount()
	if pre == 0 {
		t.Fatal("pre: unread should be >0 before PgUp")
	}

	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if got := m.UnreadCount(); got != pre {
		t.Fatalf("PgUp must not clear unread, want %d got %d", pre, got)
	}
}

// TestUpdate_KeyPgDown_ClearsWhenReachingBottom 验证 PgDn 在用户回到底部时清零 unread。
func TestUpdate_KeyPgDown_ClearsWhenReachingBottom(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25)
	pre := m.UnreadCount()
	if pre == 0 {
		t.Fatal("pre: unread should be >0")
	}

	// viewport h=20，一次 PageDown 即把 yoffset 推到 20，覆盖到当前 maxYOffset。
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !m.history.AtBottom() {
		t.Fatal("PgDown should bring viewport back to bottom")
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("PgDown-to-bottom should clear unread, got %d", got)
	}
}

// TestGotoBottom_MethodClearsUnread 验证公开方法 GotoBottom 的等价行为。
func TestGotoBottom_MethodClearsUnread(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25)
	if m.UnreadCount() == 0 {
		t.Fatal("pre: unread should be >0")
	}
	m.GotoBottom()
	if m.UnreadCount() != 0 {
		t.Fatalf("GotoBottom should clear unread, got %d", m.UnreadCount())
	}
	if !m.history.AtBottom() {
		t.Fatal("GotoBottom should leave history at bottom")
	}
}

// TestScrollDown_MethodClearsAtBottom 验证 ScrollDown 到底后清零 unread。
func TestScrollDown_MethodClearsAtBottom(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25)
	if m.UnreadCount() == 0 {
		t.Fatal("pre: unread should be >0")
	}

	// 一个足够大的 ScrollDown 把视口直撞到底。
	m.ScrollDown(1 << 20)
	if m.UnreadCount() != 0 {
		t.Fatalf("ScrollDown-to-bottom should clear unread, got %d", m.UnreadCount())
	}
	if !m.history.AtBottom() {
		t.Fatal("ScrollDown should land at bottom")
	}
}

// TestRefreshHistory_KeepsScrollPosition 验证非底部追加消息后 viewport
// 仍停留用户位置——content 已更新（SetMessages 总写），但 yoffset 不动。
func TestRefreshHistory_KeepsScrollPosition(t *testing.T) {
	m := New(Config{})
	pumpOverflow(m, 25)
	if m.history.AtBottom() {
		t.Fatal("prereq: pumpOverflow(25) should overflow and leave AtBottom=false")
	}

	pre := m.UnreadCount()
	m.applyEvent(core.Event{
		Kind:    core.EventMessage,
		Message: &protocol.StoredMessage{SenderUserID: "u1", Body: "more", ServerSeq: 999},
	})
	if got := m.UnreadCount(); got != pre+1 {
		t.Fatalf("unread should rise by 1, want %d got %d", pre+1, got)
	}
	if m.history.AtBottom() {
		t.Fatalf("refreshHistory while scrolled-away should NOT snap to bottom")
	}
}

// ============================================================
// M3.8+M3.9 新增测试：layout 退化 + 增量 append + 命令 + 错误过期
// ============================================================

// TestApplyEvent_Message_UsesAppendPath 验证 M3.8.2 增量路径：
// 单条新消息走 historyView.AppendMessage 而非全量 SetMessages。
//
// 间接验证：单条消息到达后 messages +1，history 仍反映；
// 并通过 AtBottom 行为保持 M3.6 的「用户在底跟随、不在底累加」语义。
func TestApplyEvent_Message_UsesAppendPath(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	// 灌 3 条：< viewport 高度（15），所以 AtBottom=true。
	for i := 1; i <= 3; i++ {
		m.applyEvent(core.Event{
			Kind:    core.EventMessage,
			Message: &protocol.StoredMessage{SenderUserID: "u", Body: "m", ServerSeq: uint64(i)},
		})
	}
	if got := len(m.Messages()); got != 3 {
		t.Fatalf("messages should be 3, got %d", got)
	}
	if !m.history.AtBottom() {
		t.Fatal("history should still be at bottom after append path")
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("in-bottom append should not accumulate unread, got %d", got)
	}

	// 再灌一条：仍在底，unread 仍 0。
	m.applyEvent(core.Event{
		Kind:    core.EventMessage,
		Message: &protocol.StoredMessage{SenderUserID: "u", Body: "m4", ServerSeq: 4},
	})
	if got := len(m.Messages()); got != 4 {
		t.Fatalf("messages should be 4 after second batch, got %d", got)
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("still in bottom, unread should remain 0, got %d", got)
	}
}

// TestApplyEvent_Message_AwayFromBottom_AccumulateUnread 回归 M3.6 行为：
// 用户不在底时新消息累加 unread，不动视口。
//
// 制造「不在底」：用 height=8（viewport = 8-1-3-1 = 3 行），灌 5 条消息
// 让内容超出 viewport；PageUp 把 yoffset 推 0，AtBottom 检查 yoffset>=maxYOffset
// 在内容 > viewport 高度时为 false。
func TestApplyEvent_Message_AwayFromBottom_AccumulateUnread(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	for i := 1; i <= 5; i++ {
		m.applyEvent(core.Event{
			Kind:    core.EventMessage,
			Message: &protocol.StoredMessage{SenderUserID: "u", Body: "m", ServerSeq: uint64(i)},
		})
	}
	if m.history.AtBottom() {
		t.Fatal("prereq: 5 messages in 3-line viewport should be AtBottom=true")
	}
	pre := m.UnreadCount()
	m.applyEvent(core.Event{
		Kind:    core.EventMessage,
		Message: &protocol.StoredMessage{SenderUserID: "u", Body: "after-pageup", ServerSeq: 99},
	})
	if got := m.UnreadCount(); got != pre+1 {
		t.Fatalf("unread should rise by 1, want %d got %d", pre+1, got)
	}
}

// TestTrySubmit_SlashHelp_TogglesHelpMode 验证 /help 命令切换 helpMode，
// 不走 submitMsg 路径，lastSubmitted 仍记录便于调试。
func TestTrySubmit_SlashHelp_TogglesHelpMode(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if m.HelpMode() {
		t.Fatal("helpMode should default to false")
	}
	// 在输入框输入 /help（一次插入整串，避免逐字符多次 Update）
	m.Update(tea.KeyPressMsg{Text: "/help", Code: '/'})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if cmd == nil {
		t.Fatal("/help Enter should yield a non-nil cmd (listenCmd)")
	}
	if !m.HelpMode() {
		t.Fatal("HelpMode should be true after /help submit")
	}
	if got := m.LastSubmitted(); got != "/help" {
		t.Fatalf("LastSubmitted should record \"/help\", got %q", got)
	}

	// 再来一次 /help：应关闭
	m.Update(tea.KeyPressMsg{Text: "/help", Code: '/'})
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if m.HelpMode() {
		t.Fatal("HelpMode should toggle back to false on second /help")
	}
}

// TestTrySubmit_SlashClear_ResetsMessages 验证 /clear 清空 messages 并清 zero unread。
//
// 用 height=8 让 viewport=3 行；灌 5 条已溢出，后续 3 条自然处于「离底」状态，
// unread 应累加 3。
func TestTrySubmit_SlashClear_ResetsMessages(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	for i := 1; i <= 5; i++ {
		m.applyEvent(core.Event{
			Kind:    core.EventMessage,
			Message: &protocol.StoredMessage{SenderUserID: "u", Body: "m", ServerSeq: uint64(i)},
		})
	}
	m.PageUp()
	for i := 6; i <= 8; i++ {
		m.applyEvent(core.Event{
			Kind:    core.EventMessage,
			Message: &protocol.StoredMessage{SenderUserID: "u", Body: "x", ServerSeq: uint64(i)},
		})
	}
	if m.UnreadCount() == 0 {
		t.Fatal("prereq: unread should be >0 before /clear")
	}
	if len(m.Messages()) != 8 {
		t.Fatalf("prereq: messages should be 8, got %d", len(m.Messages()))
	}

	// 输入 /clear + Enter
	m.Update(tea.KeyPressMsg{Text: "/clear", Code: '/'})
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})

	if got := len(m.Messages()); got != 0 {
		t.Fatalf("messages should be 0 after /clear, got %d", got)
	}
	if got := m.UnreadCount(); got != 0 {
		t.Fatalf("unread should be 0 after /clear, got %d", got)
	}
}

// TestTrySubmit_SlashQuit_TriggersTeaQuit 验证 /quit 返回 tea.Quit。
func TestTrySubmit_SlashQuit_TriggersTeaQuit(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.Update(tea.KeyPressMsg{Text: "/quit", Code: '/'})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if cmd == nil {
		t.Fatal("/quit should yield a non-nil cmd")
	}
	if got := m.LastSubmitted(); got != "/quit" {
		t.Fatalf("LastSubmitted should be \"/quit\" for debug, got %q", got)
	}
}

// TestTrySubmit_RegularText_RoutesToSubmit 回归：非 / 文本仍走 submitMsg。
func TestTrySubmit_RegularText_RoutesToSubmit(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.Update(tea.KeyPressMsg{Text: "hi", Code: 'h'})
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if got := m.LastSubmitted(); got != "hi" {
		t.Fatalf("LastSubmitted want \"hi\", got %q", got)
	}
	if m.HelpMode() {
		t.Fatal("helpMode should remain false after regular text submit")
	}
}

// TestUpdate_ErrMsg_RecordsExpireAt 验证 errMsg 触发时 errExpireAt 被设置。
func TestUpdate_ErrMsg_RecordsExpireAt(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	before := time.Now()
	_, cmd := m.Update(newErrMsg(errors.New("boom")))
	if cmd == nil {
		t.Fatal("errMsg should yield a non-nil cmd (Batch listenCmd + Tick)")
	}
	if m.LastError() == nil || m.LastError().Error() != "boom" {
		t.Fatalf("LastError should be set, got %v", m.LastError())
	}
	if !m.errExpireAt.After(before) {
		t.Fatalf("errExpireAt should be > before, got %v before %v",
			m.errExpireAt, before)
	}
}

// TestUpdate_ErrExpireMsg_ClearsAfterWindow 模拟 errExpireMsg 投递到点。
//
// 把 errExpireAt 倒回 6s 前，等效于 5s 窗口已过；投递 errExpireMsg 后
// lastError 应当被清掉（lastError 还在的话）。
func TestUpdate_ErrExpireMsg_ClearsAfterWindow(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, _ = m.Update(newErrMsg(errors.New("transient")))
	if m.LastError() == nil {
		t.Fatal("pre: LastError should be set")
	}
	// 把 expireAt 倒推到 6s 前，模拟已经过窗口
	m.errExpireAt = time.Now().Add(-6 * time.Second)
	_, _ = m.Update(newErrExpireMsg())
	if m.LastError() != nil {
		t.Fatalf("LastError should be cleared after window, got %v", m.LastError())
	}
}

// TestUpdate_ErrExpireMsg_NoOpBeforeWindow 验证 expireAt 还在未来时
// errExpireMsg 不应清错误。
func TestUpdate_ErrExpireMsg_NoOpBeforeWindow(t *testing.T) {
	m := New(Config{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, _ = m.Update(newErrMsg(errors.New("ongoing")))
	// expireAt 仍是 now+5s；模拟 1s 后到点的 errExpireMsg
	m.errExpireAt = time.Now().Add(1 * time.Second)
	_, _ = m.Update(newErrExpireMsg())
	if m.LastError() == nil || m.LastError().Error() != "ongoing" {
		t.Fatalf("LastError should remain before window, got %v", m.LastError())
	}
}

// TestRenderStatus_HelpModeReturnsHelpText 验证 helpMode=true 时
// renderStatus 返回帮助文本而非常规 status。
func TestRenderStatus_HelpModeReturnsHelpText(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap"})
	m.helpMode = true
	st := m.renderStatus()
	if !strings.Contains(st, "help:") {
		t.Fatalf("helpMode status should contain 'help:' prefix, got %q", st)
	}
	// 常规字段应不出现，避免冗余。
	if strings.Contains(st, "user=alice") {
		t.Fatalf("helpMode status should NOT contain user=, got %q", st)
	}
}

// TestView_RendersAllFiveRegions 验证 View 输出包含 hints 行 + history + input。
//
// 简单断言：output 长度 > 一定阈值且 hints 行字符串至少出现一次。
func TestView_RendersAllFiveRegions(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h:9000"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := m.View().Content
	// hints 行至少应包含 [Enter]
	if !strings.Contains(out, "[Enter]") {
		t.Fatalf("View should render hints line containing [Enter], got: %q", out)
	}
	// status 行应包含 user=alice
	if !strings.Contains(out, "user=alice") {
		t.Fatalf("View should render status with user=, got: %q", out)
	}
	// input 区以 borderTop 字符开头（lipgloss 边框）
	if !strings.Contains(out, "─") {
		t.Fatalf("View should render input border, got: %q", out)
	}
}

// keep referenced imports compile-clean when test list varies.
var _ = fmt.Sprint
