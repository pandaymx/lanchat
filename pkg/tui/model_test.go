package tui

import (
	"context"
	"errors"
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
	if m.history.Height() != 36 {
		t.Fatalf("history height should be h - status - input = 36, got %d",
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
