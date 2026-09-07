// Package templates 存放 lanchat web 端的 templ 模板与视图模型。
//
// 产物说明：`*_templ.go` 由 `make templ`（templ generate）生成，
// 且被 .gitignore 排除（不入库）。clone 后必须先跑 `make templ` 才能构建。
//
// 静态资源（htmx / css）不走 CDN，而由 internal/webui/assets.go 用
// embed.FS 嵌入二进制——局域网自托管场景下不依赖外网。
package templates

import (
	"fmt"
	"strconv"
	"strings"
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

// Tf 是带一个占位参数的文案查询（M7.3 typing 指示用）：
// 先取模板串再 fmt.Sprintf，tr 为 nil 时退回 key 字面值。
func Tf(tr Translator, key string, arg string) string {
	tmpl := T(tr, key)
	if tr == nil {
		return tmpl
	}
	return fmt.Sprintf(tmpl, arg)
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

// OnlineCount 是在线成员数（含自己），左侧会话列表与顶栏展示用。
func (d HomeData) OnlineCount() int { return len(d.Peers) }

// AvatarHue 由名字哈希出 0-359 的色相，头像色块背景用。
//
// 无头像系统的轻量替代：同一名字稳定同色，不同名字大概率不同色。
// 哈希用 FNV-1a（纯标准库，无依赖）。
func AvatarHue(name string) int {
	if name == "" {
		return 210 // 匿名兜底色：品牌蓝
	}
	h := uint32(2166136261)
	for _, b := range []byte(name) {
		h ^= uint32(b)
		h *= 16777619
	}
	return int(h % 360)
}

// AvatarChar 返回名字首个可见字符（用于头像块上的字母/汉字）。
func AvatarChar(name string) string {
	for _, r := range name {
		return string(r)
	}
	return "?"
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
// 与 protocol.StoredMessage 的区别：这里多了 Self（是否本人所发）、
// Read（本人消息是否已被其它设备读到，M8.1）、AtText（已格式化的
// 时间串）与 HTML（M8.2 服务端渲染的 Markdown 结果），避免在模板里
// 做逻辑判断。
type MessageView struct {
	ID         string
	Seq        int64
	SenderUser string
	Body       string
	// HTML 是 renderMarkdown 渲染出的安全 HTML（M8.2），
	// message.templ 经 templ.Raw 注入，绝不经转义路径。
	HTML   string
	AtText string
	Self   bool
	Read   bool
	// File 是附件引用（M9）；nil 表示纯文本消息。模板据此渲染
	// 文件卡片 / 图片内联预览，下载链接指向 web 自身的代理端点。
	File *protocol.FileRef
}

// HasFile 报告本消息是否带附件（模板可读性 helper）。
func (m MessageView) HasFile() bool { return m.File != nil }

// FileSizeText 把附件字节数格式化成人读尺寸（与 TUI formatSize 同口径）。
func (m MessageView) FileSizeText() string {
	return formatBytes(m.File.Size)
}

// IsImage 报告附件是否为可内联预览的图片（image/*）。
func (m MessageView) IsImage() bool {
	return m.File != nil && strings.HasPrefix(m.File.Mime, "image/")
}

// formatBytes 1024 进制人读尺寸。
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
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
// 模板层不做身份判断，保持渲染逻辑纯粹。body 在此处同步渲染成
// Markdown HTML（M8.2），模板里不再有第二处渲染入口。
func NewMessageView(id string, seq int64, sender, body string, atMs int64, self, read bool, file *protocol.FileRef) MessageView {
	return MessageView{
		ID:         id,
		Seq:        seq,
		SenderUser: sender,
		Body:       body,
		HTML:       renderMarkdown(body),
		AtText:     FormatTime(atMs),
		Self:       self,
		Read:       read,
		File:       file,
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

// TypingView 是「正在输入」指示条中一个名字的视图模型（M7.3）。
type TypingView struct {
	User string
}

// NewTypingViews 把协议 Typing 快照转成视图模型（client.Typing()
// 已按 UserID 去重排序）。
func NewTypingViews(typing []protocol.Typing) []TypingView {
	out := make([]TypingView, 0, len(typing))
	for _, t := range typing {
		out = append(out, TypingView{User: t.UserID})
	}
	return out
}

// TypingText 返回指示条文案：1 人走 web.typing.one，多人走
// web.typing.many（名字逗号连接）。放在 Go 侧拼串，templ 保持纯渲染
// （与 PageMeta.Who() 同模式）。
func TypingText(tr Translator, typing []TypingView) string {
	if len(typing) == 1 {
		return Tf(tr, "web.typing.one", typing[0].User)
	}
	names := make([]string, len(typing))
	for i, t := range typing {
		names[i] = t.User
	}
	return Tf(tr, "web.typing.many", strings.Join(names, ", "))
}
