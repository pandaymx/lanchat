package templates

// markdown.go —— M8.2：消息体 Markdown / 代码高亮渲染（服务端）。
//
// 选型（见 AGENTS.md §12 讨论结论）：goldmark 解析 + chroma 高亮，
// 纯 Go、零 CGO；与 TUI 端 glamour（同 goldmark 内核）保持一致语义。
//
// XSS 安全（提案 R9 / M8.2）：goldmark 默认不渲染原始 HTML（未开
// html.WithUnsafe），用户输入里的 <script> 等会被转义成实体输出；
// 因此这里产出的 HTML 可安全地经 templ.Raw 注入，不引入新的攻击面。
// 软换行用 WithHardWraps 渲染成 <br>，保住 TUI/Web 一直以来的
// Shift+Enter 换行习惯。

import (
	"bytes"
	"html"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	// 别名 ghtml：与 stdlib html（转义兜底用）区分，避免包名冲突。
	ghtml "github.com/yuin/goldmark/renderer/html"
)

// md 是全局共享的 Markdown 渲染器。goldmark 解析器是并发安全的
// （每次 Convert 独立分配 AST），多 SSE / HTTP goroutine 可同时用。
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM, // 表格 / 删除线 / 自动链接等 GitHub 风味
		highlighting.NewHighlighting(
			highlighting.WithStyle("monokai"), // 深色主题，token 用内联样式
		),
	),
	goldmark.WithRendererOptions(
		ghtml.WithHardWraps(), // 软换行 → <br>，保留用户显式换行
	),
)

// renderMarkdown 把消息体渲染成安全 HTML。渲染失败时退回 HTML 转义
// 的纯文本，绝不把原始输入直接当 HTML 输出。
func renderMarkdown(body string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(body), &buf); err != nil {
		return html.EscapeString(body)
	}
	return buf.String()
}
