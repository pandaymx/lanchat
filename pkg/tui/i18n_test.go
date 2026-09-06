package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestNew_DefaultUsesENT 验证 Config.Translator=nil 时 Model 仍能用 14 个
// key 的英文文案渲染（不依赖 internal/i18n bundle）。
func TestNew_DefaultUsesENT(t *testing.T) {
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h"})
	setSize(m)
	body := m.View().Content
	for _, want := range []string{
		"offline",
		"user=u",
		"device=d",
		"hub=ws://h",
		"[Enter] send",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("default EN view should contain %q, got body:\n%s", want, body)
		}
	}
}

// TestModel_RenderStatus_HelpModeUsesKey 验证 helpMode 走 tui.help.row。
//
// 注意：`/` 开头命令的路由入口是 trySubmitInput()(由 Enter KeyMsg 触发)，
// `Update(submitMsg)` 只走 Send 路径、不解析命令。所以这里把 input 直接
// 灌入 "/help"，再调 trySubmitInput() 走与键盘相同的命令路由。
func TestModel_RenderStatus_HelpModeUsesKey(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	m.input.inner.SetValue("/help")
	m.trySubmitInput() // 同包访问：直接走命令路由
	_ = m.View().Content
	if !f.called("tui.help.row") {
		t.Error("expected tui.help.row key when helpMode=true")
	}
}

// TestModel_RenderStatus_OfflineLabels 验证未连 hub 时 offline + 4 个 label 都走 translator。
func TestModel_RenderStatus_OfflineLabels(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	_ = m.View().Content
	for _, k := range []string{
		"tui.status.offline",
		"tui.status.label.user",
		"tui.status.label.device",
		"tui.status.label.hub",
		"tui.hints.row",
	} {
		if !f.called(k) {
			t.Errorf("expected translator key %q to be called", k)
		}
	}
}

// TestModel_RenderStatus_OnlineLabel 验证 connected=true 时 online key 被调用。
//
// 注意：renderStatus 里 conn = m.t("offline") 先无条件执行，connected=true
// 时再 m.t("online") 覆盖。所以 offline key 仍会被调用，但 fake 返回的
// raw 串被覆盖——这是预期行为。测试只断言 "online 被调 + View body 含 online"。
func TestModel_RenderStatus_OnlineLabel(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	m.applyEvent(core.Event{Kind: core.EventState, State: &core.StateInfo{Connected: true}})
	_ = m.View().Content
	if !f.called("tui.status.online") {
		t.Error("expected online key when connected=true")
	}
	body := m.View().Content
	if !strings.Contains(body, "tui.status.online") {
		t.Error("expected body to contain online label (raw key from fake)")
	}
}

// TestModel_RenderStatus_UnreadLabel 验证 unread > 0 时 unread label 被调用。
//
// 要让 unread++ 触发，需要 applyEvent 前的 wasAtBottom == false。
// viewport 的 maxYOffset 与实际内容行数相关：内容低于视口高度时 maxYOffset=0，
// ScrollUp(100) 会立即 clamp 回底部、AtBottom 永远为 true。所以测试里把
// viewport 缩到 2 行 + seed 5 条消息，让 maxYOffset > 0，ScrollUp 才能滚出底部。
func TestModel_RenderStatus_UnreadLabel(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	// 把 history viewport 压到 2 行高，让少量消息就能滚出底部。
	m.history.SetSize(40, 2)
	// seed: 5 条历史消息填满视口并溢出。
	for i := 0; i < 5; i++ {
		seedMsg := protocol.StoredMessage{
			ID:             fmt.Sprintf("s%d", i),
			Body:           "seed",
			ServerSeq:      uint64(i),
			CreatedAt:      int64(i+1) * 1000,
			SenderUserID:   "alice",
			SenderDeviceID: "laptop",
		}
		m.applyEvent(core.Event{Kind: core.EventMessage, Message: &seedMsg})
	}
	if !m.history.AtBottom() {
		t.Skipf("seed didn't leave history at bottom (maxYOffset may not have updated)")
	}
	m.history.ScrollUp(100)
	if m.history.AtBottom() {
		t.Fatalf("ScrollUp should leave history above bottom once content overflows viewport")
	}
	msg := protocol.StoredMessage{ID: "m1", Body: "hello", ServerSeq: 99, SenderUserID: "bob"}
	m.applyEvent(core.Event{Kind: core.EventMessage, Message: &msg})
	_ = m.View().Content
	if m.unread < 1 {
		t.Fatalf("expected unread > 0 after msg while scrolled up, got %d", m.unread)
	}
	if !f.called("tui.status.label.unread") {
		t.Error("expected unread label when unread > 0")
	}
}

// TestModel_RenderStatus_ErrLabel 验证 lastError != nil 时 err label 被调用。
//
// errMsg 通过 inbox → Update → case errMsg 设 lastError；不走 applyEvent
// EventState.Err（applyEvent 只设 connected）。
func TestModel_RenderStatus_ErrLabel(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	_, _ = m.Update(errMsg{err: errFake("boom")})
	_ = m.View().Content
	if !f.called("tui.status.label.err") {
		t.Error("expected err label when lastError != nil")
	}
}

// TestModel_RenderSidebar_UsesTranslator 验证 peers=0 时走 sidebar.empty。
func TestModel_RenderSidebar_UsesTranslator(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	_ = m.View().Content
	if !f.called("tui.sidebar.empty") {
		t.Error("expected sidebar.empty key when peers is empty")
	}
}

// TestModel_RenderSidebar_PrefixKey 验证有 peers 时走 sidebar.prefix。
func TestModel_RenderSidebar_PrefixKey(t *testing.T) {
	f := newFakeTranslator(nil)
	m := New(Config{User: "u", Device: "d", HubURL: "ws://h", Translator: f})
	setSize(m)
	m.applyEvent(core.Event{Kind: core.EventPresence, Presence: &protocol.Presence{
		UserID: "alice", DeviceID: "laptop", Online: true,
	}})
	_ = m.View().Content
	if !f.called("tui.sidebar.prefix") {
		t.Error("expected sidebar.prefix key when peers > 0")
	}
	if f.called("tui.sidebar.empty") {
		t.Error("sidebar.empty should not be called when peers > 0")
	}
}

// TestNewTextInput_PlaceholderUsesTranslator 验证 textarea 占位符走 translator。
func TestNewTextInput_PlaceholderUsesTranslator(t *testing.T) {
	f := newFakeTranslator(map[string]string{
		"tui.input.placeholder": "Type here!",
	})
	ti := newTextInput(f)
	if got := ti.inner.Placeholder; got != "Type here!" {
		t.Errorf("placeholder = %q, want %q", got, "Type here!")
	}
	if !f.called("tui.input.placeholder") {
		t.Error("expected placeholder key to be called")
	}
}

// TestNewTextInput_NilUsesNop 验证 tr=nil 时仍能构造（用 nopTranslator fallback）。
func TestNewTextInput_NilUsesNop(t *testing.T) {
	ti := newTextInput(nil)
	if got := ti.inner.Placeholder; got != "tui.input.placeholder" {
		t.Errorf("nil translator should return key, got %q", got)
	}
}

// TestNewHistoryView_FallbackKeys 验证无 SenderUserID/DeviceID 时走 fallback.user。
func TestNewHistoryView_FallbackKeys(t *testing.T) {
	f := newFakeTranslator(nil)
	hv := newHistoryView(f)
	got := formatMessage(&hv, protocol.StoredMessage{Body: "hi", CreatedAt: 0})
	// fake 返回 raw key，所以 output 含 "tui.history.fallback.user" / "tui.history.fallback.time"
	if !strings.Contains(got, "tui.history.fallback.user") {
		t.Errorf("formatMessage fallback should call fallback.user, got %q", got)
	}
	if !strings.Contains(got, "tui.history.fallback.time") {
		t.Errorf("formatMessage fallback should call fallback.time, got %q", got)
	}
	if !f.called("tui.history.fallback.user") {
		t.Error("expected history.fallback.user key")
	}
	if !f.called("tui.history.fallback.time") {
		t.Error("expected history.fallback.time key")
	}
}

// TestDefaultENTranslator_AllKeysResolve 验证 defaultENTranslator 14 个 key 都有值。
func TestDefaultENTranslator_AllKeysResolve(t *testing.T) {
	tr := defaultENTranslator{}
	for _, k := range []string{
		"tui.input.placeholder",
		"tui.input.help.newline",
		"tui.status.online",
		"tui.status.offline",
		"tui.status.label.user",
		"tui.status.label.device",
		"tui.status.label.hub",
		"tui.status.label.unread",
		"tui.status.label.err",
		"tui.hints.row",
		"tui.help.row",
		"tui.sidebar.empty",
		"tui.sidebar.prefix",
		"tui.history.fallback.user",
		"tui.history.fallback.time",
		"tui.typing.one",
		"tui.typing.many",
	} {
		v := tr.T(k)
		if v == k {
			t.Errorf("defaultENTranslator missing value for %q", k)
		}
	}
}

// TestFakeTranslator_CalledAndCount 验证 fakeTranslator 的 called / calledCount helper。
func TestFakeTranslator_CalledAndCount(t *testing.T) {
	f := newFakeTranslator(nil)
	_ = f.T("a")
	_ = f.T("b")
	_ = f.T("a")
	if !f.called("a") {
		t.Error("a should be called")
	}
	if !f.called("b") {
		t.Error("b should be called")
	}
	if f.called("c") {
		t.Error("c should not be called")
	}
	if got := f.calledCount("a"); got != 2 {
		t.Errorf("a called count = %d, want 2", got)
	}
}

// TestFakeTranslator_ResponseMap 验证预设 response 时返回响应而非 key。
func TestFakeTranslator_ResponseMap(t *testing.T) {
	f := newFakeTranslator(map[string]string{"k": "v"})
	if got := f.T("k"); got != "v" {
		t.Errorf("response lookup = %q, want %q", got, "v")
	}
	if got := f.T("missing"); got != "missing" {
		t.Errorf("missing should return key, got %q", got)
	}
}

// errFake 是一个稳定的 error 实例，给 TestModel_RenderStatus_ErrLabel 用。
type errFake string

func (e errFake) Error() string { return string(e) }

// setSize 是给 Model 设 width/height 让 View 渲染时不强制换行的 helper。
func setSize(m *Model) {
	m.width = 100
	m.height = 30
}
