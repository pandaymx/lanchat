package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/core"
)

// eventMsg 把 core.Event 包成 tea.Msg，让 client 的事件总线消息
// 经 inbox 通道投递给 bubbletea 的 Update。
type eventMsg struct {
	event core.Event
}

// errMsg 把非致命错误投递给 Update，例如 store 写失败、解析失败。
type errMsg struct {
	err error
}

// quitMsg 表示主动退出请求（Ctrl+C / 业务 Quit 命令 / inbox 关闭）。
type quitMsg struct {
	reason string
}

// submitMsg 表示用户在 textarea 中按下 Enter 后提交的文本。
//
// 数据流：Model.Update 在收到未带 ModShift 的 Enter / Ctrl+M 时，
// 取出 textarea 当前 Value、Reset，然后把文本打包成本 Msg 投入 inbox。
// 这样提交行为本身仍是「事件 → inbox → Update」的形式，保持与
// eventMsg / errMsg / quitMsg 完全一致的协议边界；进一步路由（写入
// 客户端 Send）由 M3.5+ 的 client adapter 在 Update 的 switch 中处理。
type submitMsg struct {
	text string
}

func newEventMsg(e core.Event) eventMsg { return eventMsg{event: e} }
func newErrMsg(err error) errMsg        { return errMsg{err: err} }
func newQuitMsg(reason string) quitMsg  { return quitMsg{reason: reason} }
func newSubmitMsg(text string) submitMsg {
	return submitMsg{text: text}
}

// 编译期断言：全部适配类型都满足 tea.Msg。
var (
	_ tea.Msg = eventMsg{}
	_ tea.Msg = errMsg{}
	_ tea.Msg = quitMsg{}
	_ tea.Msg = submitMsg{}
)
