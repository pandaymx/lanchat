// Package templates 存放 lanchat web 端的 templ 模板与视图模型。
//
// 产物说明：`*_templ.go` 由 `make templ`（templ generate）生成，
// 且被 .gitignore 排除（不入库）。clone 后必须先跑 `make templ` 才能构建。
//
// 静态资源（htmx / css）不走 CDN，而由 internal/webui/assets.go 用
// embed.FS 嵌入二进制——局域网自托管场景下不依赖外网。
package templates

import (
	"strconv"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// PageMeta 是 base 模板需要的公共 head 信息。
type PageMeta struct {
	Title   string
	User    string
	Device  string
	Version string
}

// Translator 是模板渲染时的 UI chrome 文案查询接口。
//
// 由 internal/webui 在装配期注入（cmd/web 启动时把 internal/i18n 的
// bundle.ForLocale 结果传进来）；templates 不 import internal/i18n，
// 保持模板包无业务依赖，测试也可以塞任意 fake。
// 接口形状与 pkg/tui.Translator 一致（照 AGENTS.md §13 注入模式）。
type Translator interface {
	T(key string) string
}

// T 是模板里的文案查询入口：tr 为 nil（装配遗漏）时返回 key 字面值——
// 与 i18n.Bundle 缺 key 的 fallback 行为一致，页面至少不 panic、不出空串。
func T(tr Translator, key string) string {
	if tr == nil {
		return key
	}
	return tr.T(key)
}

// Who 返回 "user@device" 形式的身份串。
//
// 为什么不在 templ 里写 `{ meta.User }@{ meta.Device }`：templ 把 `@` 当作
// 组件调用前缀（`@base(...)` 那种语法），裸 `@` 会导致 "expected operand,
// found '{'" 解析错误。放进 Go 侧拼串最省事，也让模板保持纯渲染。
func (p PageMeta) Who() string {
	return p.User + "@" + p.Device
}

// HomeData 是 home 模板的渲染入参。
//
// HasMore/OldestSeq 驱动首屏「加载更早消息」按钮：首屏拉满 historyLimit
// 条时认为可能还有更早的消息，OldestSeq 是当前最老一条的 ServerSeq，
// 作为第一个 /history?before= 的游标。
type HomeData struct {
	Meta      PageMeta
	Messages  []MessageView
	Peers     []PeerView
	Connected bool
	Error     string
	HasMore   bool
	OldestSeq int64
	// Tr 是 UI chrome 文案翻译器；由 cmd/web 装配期注入 i18n bundle，
	// 缺省 nil 时 T() 兜底返回 key 字面值。
	Tr Translator
}

// HistoryHref 构造「加载更早消息」按钮的 hx-get URL。
//
// 放在 Go 侧拼串：templ 模板里不引 strconv，保持模板纯渲染
// （同 PageMeta.Who() 的考量）。
func HistoryHref(before int64) string {
	return "/history?before=" + strconv.FormatInt(before, 10)
}

// MessageView 是单条消息的视图模型。
//
// 与 protocol.StoredMessage 的区别：这里多了 Self（是否本人所发）与
// AtText（已格式化的时间串），避免在模板里做逻辑判断。
type MessageView struct {
	ID         string
	Seq        int64
	SenderUser string
	Body       string
	AtText     string
	Self       bool
}

// FormatTime 把 Unix 毫秒格式化为 HH:MM:SS（本地时区）。
//
// TUI 端（pkg/tui/history.go 的 formatUnixMilli）用的是同样格式，
// 两端观感保持一致。ms<=0 视为"无时间"，返回占位串。
func FormatTime(ms int64) string {
	if ms <= 0 {
		return "??:??:??"
	}
	return time.UnixMilli(ms).Local().Format("15:04:05")
}

// NewMessageView 从协议消息构造视图模型。
//
// self 由调用方（handler）比较 SenderUser 与当前会话 user 得出——
// 模板层不做身份判断，保持渲染逻辑纯粹。
func NewMessageView(id string, seq int64, sender, body string, atMs int64, self bool) MessageView {
	return MessageView{
		ID:         id,
		Seq:        seq,
		SenderUser: sender,
		Body:       body,
		AtText:     FormatTime(atMs),
		Self:       self,
	}
}

// PeerView 是在线成员列表中一行的视图模型（M7.2）。
//
// 名单只含在线设备（offline 在 client 层已剔除）；Self 标记当前会话
// 自己的设备，模板据此渲染 "(you)"。
type PeerView struct {
	User   string
	Device string
	Self   bool
}

// NewPeerViews 把协议 Presence 名单转成视图模型，按传入顺序渲染
// （client.Peers() 已按 DeviceID 排序，结果稳定）。
// selfDevice 非空时匹配到的条目标记 Self。
func NewPeerViews(peers []protocol.Presence, selfDevice string) []PeerView {
	out := make([]PeerView, 0, len(peers))
	for _, p := range peers {
		out = append(out, PeerView{
			User:   p.UserID,
			Device: p.DeviceID,
			Self:   selfDevice != "" && p.DeviceID == selfDevice,
		})
	}
	return out
}
