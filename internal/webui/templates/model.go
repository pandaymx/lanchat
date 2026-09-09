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

	// M12-A 群聊：会话列表与当前会话。Convs 含合成的大厅（排第一）；
	// ConvID 是当前渲染的会话 ID（空 = 大厅）；ConvTitle 供顶栏显示。
	Convs      []ConvView
	ConvID     string
	ConvTitle  string
	ConvMember bool // 当前用户是否为当前会话成员（大厅恒 true）
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
	// ConvID 是消息所属会话（v1.1）：SSE 帧渲染的 <li> 带 data-conv，
	// app.js 据此区分「当前会话」与「其它会话」，做未读计数。
	ConvID string
	// AtMs 是消息的 Unix 毫秒时间（v1.3）：列表组装时据此生成
	// 时间分隔线（ShowDivider/DividerText 由 MarkDayDividers 填充）。
	AtMs int64
	// ShowDivider/DividerText（v1.3）：本消息前插入一条日期分隔线
	// （今天 / 昨天 / 具体日期）。SSE 单条推送恒为 false——新消息
	// 一定落在"今天"，列表场景才需要分隔。
	ShowDivider bool
	DividerText string
	// Reply 非空表示该消息是引用回复（v1.1）。快照由发送端构造，
	// 渲染引用块无需再查库；nil = 普通消息。
	Reply *protocol.ReplyRef
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

// IsAudio 报告附件是否为语音消息（audio/*，v1.3）：Web 端渲染成
// 语音条（播放 / 波形 / 时长）。语音与图片共用文件通道（FileRef），
// 区别仅在 Mime——协议零改动。
func (m MessageView) IsAudio() bool {
	return m.File != nil && strings.HasPrefix(m.File.Mime, "audio/")
}

// VoiceWaveHeights 返回语音条波形条的高度（% of bar max，v1.3）。
//
// 由 FileID 哈希出 24 个稳定高度（30-100%），同一语音永远同波形；
// 纯展示装饰，不含真实音频能量。模板据此渲染 <i> 的 style="height"。
func VoiceWaveHeights(fid string) []int {
	out := make([]int, 24)
	h := uint32(2166136261)
	for _, b := range []byte(fid) {
		h ^= uint32(b)
		h *= 16777619
	}
	for i := range out {
		h = h*1664525 + 1013904223 // LCG 递推，避免全部取低 4 位雷同
		out[i] = 30 + int(h%71)
	}
	return out
}

// MarkDayDividers 在消息列表上按自然日插入分隔线（v1.3）。
//
// 遍历时与上一条比较日期：日期变化则在当前消息置 ShowDivider。
// 列表必须是升序（时间从旧到新）；SSE 单条推送不调用本函数。
func MarkDayDividers(tr Translator, views []MessageView) {
	prevDay := ""
	for i := range views {
		day := dayKey(views[i].AtMs)
		if day != prevDay {
			views[i].ShowDivider = true
			views[i].DividerText = dayDividerText(tr, views[i].AtMs)
			prevDay = day
		}
	}
}

func dayKey(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02")
}

// dayDividerText 把 Unix 毫秒转成分隔线文案：今天 / 昨天 / 具体日期。
// 时间比较用本地时区；非今昨的日期统一显示 ISO 格式（2006-01-02），
// 语言无关，避免 i18n 格式串与 fmt 动词的耦合（%b 会被 Go 解释成
// 二进制）。今天/昨天文案来自 i18n（web.day.today / web.day.yesterday）。
func dayDividerText(tr Translator, ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).Local()
	now := time.Now()
	switch {
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return T(tr, "web.day.today")
	case t.Add(24*time.Hour).Year() == now.Year() && t.Add(24*time.Hour).YearDay() == now.YearDay():
		return T(tr, "web.day.yesterday")
	default:
		return t.Format("2006-01-02")
	}
}

// ReplyPreview 是引用块的展示文案（v1.1）：「sender: 正文预览」。
// 预览截断到 60 runes 且把换行压成空格——引用块必须是单行小字，
// 多行正文会把气泡撑变形。
func (m MessageView) ReplyPreview() string {
	if m.Reply == nil {
		return ""
	}
	return m.Reply.SenderUserID + ": " + clipRunes(m.Reply.Body, 60)
}

// ReplyBodyShort 是回复按钮 data-reply-body 的短正文（v1.1）：
// 发送端点击「回复」时 app.js 读到它构造 ReplyRef 快照。截断到
// 80 runes，换行压空格，保证 data 属性是单行可转义文本。
func (m MessageView) ReplyBodyShort() string {
	if m.Reply == nil {
		return clipRunes(m.Body, 80)
	}
	return clipRunes(m.Reply.Body, 80)
}

// clipRunes 把 s 截断到最多 n 个 rune，并把 CR/LF/Tab 统一压成空格。
func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	flat := strings.Join(strings.Fields(s), " ")
	r := []rune(flat)
	if len(r) <= n {
		return flat
	}
	return string(r[:n]) + "…"
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
		AtMs:       atMs,
		Self:       self,
		Read:       read,
		File:       file,
	}
}

// ConvView 是会话列表一行的视图模型（M12-A）。
//
// Active 标记当前选中的会话；大厅（空 ID）由 handler 合成并恒排第一。
type ConvView struct {
	ID     string
	Title  string
	Kind   string // "lobby" | "group"
	Active bool
	// MemberCount 是群成员数（大厅为 0，不展示）。
	MemberCount int
}

// NewConvViews 把协议会话快照转成视图模型，大厅排第一、其余按 ID 升序
// （handler 已保证传入顺序）；activeConvID 命中的条目标记 Active。
func NewConvViews(snaps []protocol.ConversationSnapshot, activeConvID string) []ConvView {
	out := make([]ConvView, 0, len(snaps)+1)
	// 合成大厅（协议层不落库，客户端恒可见）。
	out = append(out, ConvView{ID: "", Title: "Lobby", Kind: "lobby", Active: activeConvID == ""})
	for _, s := range snaps {
		if s.Conversation.ID == "" {
			continue // 防御：不重复渲染大厅
		}
		out = append(out, ConvView{
			ID:          s.Conversation.ID,
			Title:       s.Conversation.Title,
			Kind:        s.Conversation.Kind,
			Active:      s.Conversation.ID == activeConvID,
			MemberCount: len(s.Members),
		})
	}
	return out
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

// NewTypingViews 把协议 Typing 快照转成视图模型（M7.3 + M12-A）。
// 只保留 convID 会话的条目（client.Typing() 是全量快照，已按 UserID
// 去重排序）；convID 为空 = 大厅。
func NewTypingViews(typing []protocol.Typing, convID string) []TypingView {
	out := make([]TypingView, 0, len(typing))
	for _, t := range typing {
		if t.ConversationID != convID {
			continue
		}
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
