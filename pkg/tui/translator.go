package tui

// Translator 是面向 UI chrome 的本地化接口。
//
// 详细方案见 docs/proposals/2026-09-05-i18n-proposal.md；本接口与
// internal/i18n.Translator 同形（都是 T(key) string），但定义在
// pkg/tui 包内，理由：
//
//   - pkg/ 不能 import internal/（Go 模块边界约定）；
//   - 由 cmd/tui 启动期把 internal/i18n.Bundle.ForLocale(...) 注入。
//
// 测试可以用 fakeTranslator 断言 key 是否被调用，而无需 bundle 文件。
type Translator interface {
	T(key string) string
}

// nopTranslator 是 Translator 的零值 fallback：返回 key 串本身。
//
// 用于 Model 默认构造（Config.Translator 为 nil）以及单测 base 场景。
// 让"未注入 translator"的代码仍能编译运行，只是 UI 显示原始 key 串——
// 开发期显眼、测试期可控。
type nopTranslator struct{}

// T 实现 Translator 接口；nil-safe（无需判 receiver）。
func (nopTranslator) T(key string) string { return key }

// fakeTranslator 是测试专用：把所有 T(key) 调用记录到 keys，
// 并返回预置的 responses[locale][key]（未预设时返回 key）。
//
// 用法：
//
//	f := &fakeTranslator{responses: map[string]map[string]string{
//	    "zh-CN": {"tui.status.online": "在线"},
//	}}
//	m := New(Config{Translator: f, ...})
//	_ = m.View().Content
//	if !f.called("tui.status.online") { t.Fatal("key not used") }
type fakeTranslator struct {
	responses map[string]map[string]string
	locale    string
	keys      []string
}

// newFakeTranslator 构造一个默认 locale=en 的 fake translator。
func newFakeTranslator(responses map[string]string) *fakeTranslator {
	if responses == nil {
		responses = map[string]string{}
	}
	return &fakeTranslator{
		locale:    "en",
		responses: map[string]map[string]string{"en": responses},
	}
}

// T 实现 Translator 接口；记录每次调用的 key 并按 locale 查表返回。
func (f *fakeTranslator) T(key string) string {
	f.keys = append(f.keys, key)
	if m, ok := f.responses[f.locale]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return key
}

// called 报告 key 是否曾被 T 调用过。
func (f *fakeTranslator) called(key string) bool {
	for _, k := range f.keys {
		if k == key {
			return true
		}
	}
	return false
}

// calledCount 报告 key 被调用过的次数（同一 key 多次渲染算多次）。
func (f *fakeTranslator) calledCount(key string) int {
	n := 0
	for _, k := range f.keys {
		if k == key {
			n++
		}
	}
	return n
}

// SetLocale 切换 fake translator 的 locale；用于多 locale 测试。
func (f *fakeTranslator) SetLocale(locale string) { f.locale = locale }

// enDefaults 是 pkg/tui 内置 key 的英文文案，与
// internal/i18n/bundles/en.json 完全一致。这里独立保留一份的理由：
//
//   - pkg/tui 不依赖 internal/（Go 模块边界约定）；
//   - 单元测试无需每处都构造 fake translator，New() 直接拿这份做默认。
//
// 真实 cmd/tui 启动时由 i18n.Bundle.ForLocale 覆盖。设计要点见
// docs/proposals/2026-09-05-i18n-proposal.md §4.4.1。
var enDefaults = map[string]string{
	"tui.input.placeholder":     "type a message (Enter to send, Shift+Enter for newline)",
	"tui.input.help.newline":    "insert newline",
	"tui.status.online":         "online",
	"tui.status.offline":        "offline",
	"tui.status.label.user":     "user",
	"tui.status.label.device":   "device",
	"tui.status.label.hub":      "hub",
	"tui.status.label.unread":   "unread",
	"tui.status.label.err":      "err",
	"tui.hints.row":             "[Enter] send · [Shift+Enter] newline · [End] tail · [PgUp/PgDn] scroll · [/help] commands · [Ctrl+C] quit",
	"tui.help.row":              "help: Enter send · Shift+Enter newline · End tail · PgUp/PgDn scroll · /help · /clear · /quit · Ctrl+C quit",
	"tui.sidebar.empty":         "peers: (none yet)",
	"tui.sidebar.prefix":        "peers:",
	"tui.history.fallback.user": "?",
	"tui.history.fallback.time": "??:??:??",
	// M7.3：hints 行让位的「正在输入」指示，%s 是逗号拼接的用户名。
	"tui.typing.one":  "%s is typing…",
	"tui.typing.many": "%s are typing…",
	// M8.1：自己消息被他人读到后的「✓已读」标记（英文同勾号）。
	"tui.history.read": "✓ read",
}

// defaultENTranslator 是 Model.New() 默认注入的 translator，
// 直接从 enDefaults map 查表返回。无外部依赖，14 处 UI 文案
// 全部自带英文文案。
type defaultENTranslator struct{}

// T 实现 Translator 接口，直接读 enDefaults map。
// nil-safe（无需判 receiver）；enDefaults 缺失 key 返回 key 串。
func (defaultENTranslator) T(key string) string {
	if v, ok := enDefaults[key]; ok {
		return v
	}
	return key
}
