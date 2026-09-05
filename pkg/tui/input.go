package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// 默认宽度 / 高度；SetSize 由 Model.Update 在 WindowSizeMsg 时覆盖。
const (
	defaultInputWidth  = 40
	defaultInputHeight = 3
	maxMessageChars    = 4096
)

// textInput 封装 textarea.Model，对外暴露项目内部的最小 API。
//
// 键位策略：textarea 的 KeyMap.InsertNewline 默认绑到 "enter" + "ctrl+m"。
// 为实现「Enter 提交 / Shift+Enter 换行」语义，构造时把 InsertNewline 重绑
// 到 "shift+enter" + "ctrl+j"。于是裸 Enter 落到本组件 default 分支不做事，
// 由 outer Model.Update 拦截后负责清空 + 投递 submitMsg。
//
// 注：小键盘 Enter (KeyKpEnter) 与主键盘 Enter 在 bubbles key binding 字符串
// 层面无法区分（bubbletea 两者都 stringify 为 "enter"），但 outer Model 读到
// 的 Key.Code 是不同 rune。本组件不需要单独处理 kpenter——outer 把小键盘 Enter
// 也路由到 trySubmitInput / InsertNewline，与主键盘 Enter 行为一致。
type textInput struct {
	inner textarea.Model
}

// newTextInput 用合理默认值构造一个 textInput。
//
// 占位符提示、字符上限 4096（一段长代码片段够用且不会撑爆内存）；
// ShowLineNumbers 关闭，背景行不画数字，UI 更紧凑。
//
// 关闭 virtual cursor：bubbles/textarea 默认用 "virtual cursor"（用样式自己画
// 光标、隐藏真实终端光标）。在 Windows 终端 + 中文输入法场景下，IME 候选框
// 需要跟随真实终端光标；virtual cursor 会让 IME 候选框漂到窗口右下角。
// 关闭后 textarea 会通过 Cursor() 返回真实光标位置，bubbletea 把它同步给
// 终端，输入法候选框就能正确跟到输入位置。
func newTextInput() textInput {
	ti := textarea.New()
	ti.Placeholder = "type a message (Enter to send, Shift+Enter for newline)"
	ti.CharLimit = maxMessageChars
	ti.ShowLineNumbers = false
	ti.SetWidth(defaultInputWidth)
	ti.SetHeight(defaultInputHeight)
	// 真实光标（非 virtual cursor），让 Windows 终端 IME 候选框跟随输入位置。
	ti.SetVirtualCursor(false)
	ti.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "insert newline"),
	)
	return textInput{inner: ti}
}

// Init 是占位实现。textarea.Model 不暴露 Init 方法（New 后即可使用），
// 保留此方法是为了让 textInput 与 historyView 形状一致、外层统一调用。
func (t *textInput) Init() tea.Cmd {
	return nil
}

// Update 把消息透传给底层 textarea；不修改外层状态。
func (t *textInput) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	t.inner, cmd = t.inner.Update(msg)
	return cmd
}

// View 返回当前字符串画面，由 outer 用 lipgloss 拼装。
func (t *textInput) View() string {
	return t.inner.View()
}

// Value 返回当前输入文本；Model 在 Enter 提交时读取后立即 Reset。
func (t *textInput) Value() string {
	return t.inner.Value()
}

// Reset 清空输入框。提交成功后调用，让用户可以继续输入下一条。
func (t *textInput) Reset() {
	t.inner.Reset()
}

// Focus 把光标放到输入框；M3.4 启动后调用。
func (t *textInput) Focus() tea.Cmd {
	return t.inner.Focus()
}

// Blur 失焦；保留内容但停止接收键事件。
func (t *textInput) Blur() {
	t.inner.Blur()
}

// Focused 报告当前是否获得焦点；用于 outer 决定键路由。
func (t *textInput) Focused() bool {
	return t.inner.Focused()
}

// SetSize 把 width/height 同步给底层 textarea。
// outer Model 在 WindowSizeMsg 时按可用区域算出 w/h 传入。
func (t *textInput) SetSize(w, h int) {
	t.inner.SetWidth(w)
	t.inner.SetHeight(h)
}

// Width returns the underlying textarea width, mainly for tests.
func (t *textInput) Width() int {
	return t.inner.Width()
}

// Height returns the underlying textarea height, mainly for tests.
func (t *textInput) Height() int {
	return t.inner.Height()
}
