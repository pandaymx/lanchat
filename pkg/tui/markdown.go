package tui

// markdown.go —— M8.2：消息体 Markdown → ANSI 渲染（glamour）。
//
// 选型（见 AGENTS.md §12 讨论结论）：glamour 是 bubbletea 生态的标准
// Markdown 渲染器，与 Web 端（internal/webui/templates/markdown.go 的
// goldmark）同内核，两端对同一段 Markdown 的解析语义一致；glamour
// 渲染前还会用 bluemonday 清洗 HTML 节点（探针确认 <script> 整段被
// 清空），终端场景同样防注入。纯 Go、零 CGO，不破坏 AGENTS.md §11
// 的交叉编译约束。
//
// 消息行宽与 history 的 messageLineWrap 保持一致：先截断再渲染，
// 截断点不会把 ANSI 码算进长度。

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
)

// mdRenderer 是包级共享的渲染器。glamour 的 Render 每次调用独立，
// 无共享可变状态，TUI 单 goroutine 下天然安全。
var mdRenderer = newMDRenderer()

// newMDRenderer 构造 glamour 渲染器。以 dark 为基础做三处聊天适配
// （探针验证过的输出形态，见 pkg/tui/read_test.go 的 Markdown 用例）：
//
//  1. WordWrap(0)：关闭段落「填充到整行宽」（默认会把每行 pad 成 200
//     列带样式空格，聊天场景纯属噪声），也关闭自动换行——行宽交给
//     viewport 裁切，与 M8.1 及更早的纯文本行为一致；
//  2. Document/Paragraph 的 Margin、Indent、Block 前后缀清零：避免
//     每条消息前出现空行、缩进与空样式 span，正文无前缀地接在
//     `[ts] user: ` 后面；
//  3. Document/Paragraph 不染色：纯文本消息渲染结果与旧版逐字一致，
//     颜色只出现在 markdown 显式标记处（加粗、行内代码、代码块、
//     标题、链接等）。
func newMDRenderer() *glamour.TermRenderer {
	cfg := styles.DarkStyleConfig
	zero := func(v uint) *uint { return &v }

	cfg.Document.Margin = zero(0)
	cfg.Document.Indent = zero(0)
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	cfg.Document.Color = nil

	cfg.Paragraph.Margin = zero(0)
	cfg.Paragraph.Indent = zero(0)
	cfg.Paragraph.BlockPrefix = ""
	cfg.Paragraph.BlockSuffix = ""
	cfg.Paragraph.Color = nil

	cfg.CodeBlock.Margin = zero(0)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return nil
	}
	return r
}

// mdRender 把 Markdown 渲染成 ANSI 文本（去掉渲染器自带的收尾换行，
// 让调用方自行拼行）；渲染器缺失或渲染失败时原样返回输入，降级为
// 纯文本展示。
func mdRender(body string) string {
	if mdRenderer == nil {
		return body
	}
	out, err := mdRenderer.Render(body)
	if err != nil {
		return body
	}
	return strings.TrimRight(out, "\n")
}
