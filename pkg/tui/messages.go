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

func newEventMsg(e core.Event) eventMsg { return eventMsg{event: e} }
func newErrMsg(err error) errMsg        { return errMsg{err: err} }
func newQuitMsg(reason string) quitMsg  { return quitMsg{reason: reason} }

// 编译期断言：全部适配类型都满足 tea.Msg。
var (
	_ tea.Msg = eventMsg{}
	_ tea.Msg = errMsg{}
	_ tea.Msg = quitMsg{}
)
