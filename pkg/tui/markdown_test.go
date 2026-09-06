package tui

// markdown_test.go —— M8.2：glamour 渲染的收敛性测试。
//
// 这些断言依赖 newMDRenderer 里的三处聊天适配（纯文本不染色、
// 无填充无缩进、WordWrap 0），改配置前先看探针记录。

import (
	"strings"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestMdRender_PlainTextUnchanged 验证纯文本消息渲染结果与旧版逐字
// 一致（无 ANSI、无前后缀、无截断）。URL 除外——glamour 按 GFM
// 自动链接给 URL 加链接样式，那是 M8.2 期望行为，见
// TestMdRender_Autolink。
func TestMdRender_PlainTextUnchanged(t *testing.T) {
	for _, in := range []string{"hi", "plain & text", "a < b", "x 1 \u2264 2"} {
		if got := mdRender(in); got != in {
			t.Errorf("mdRender(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestMdRender_Autolink 验证裸 URL 被识别成链接（内容保留、带样式）。
func TestMdRender_Autolink(t *testing.T) {
	out := mdRender("http://x.com/y")
	if !strings.Contains(out, "http://x.com/y") {
		t.Errorf("URL text lost: %q", out)
	}
}

// TestMdRender_Formats 验证 markdown 显式标记被渲染：
// 加粗与行内代码的标记字符消失、内容保留。
func TestMdRender_Formats(t *testing.T) {
	out := mdRender("**bold** and `code`")
	if strings.Contains(out, "**") {
		t.Errorf("bold markers leaked: %q", out)
	}
	if strings.Contains(out, "`") {
		t.Errorf("code markers leaked: %q", out)
	}
	for _, want := range []string{"bold", "code"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

// TestMdRender_CodeBlock 验证围栏代码块被渲染（围栏消失、代码保留，
// 含高亮 ANSI）。
func TestMdRender_CodeBlock(t *testing.T) {
	out := mdRender("```go\npackage main\n```")
	if strings.Contains(out, "```") {
		t.Errorf("fence leaked: %q", out)
	}
	for _, want := range []string{"package", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("code block missing %q: %q", want, out)
		}
	}
}

// TestMdRender_StripsRawHtml 验证原始 HTML 不穿透：bluemonday 把
// <script> 整段清空（终端场景同样防注入）。
func TestMdRender_StripsRawHtml(t *testing.T) {
	out := mdRender("<script>alert(1)</script>")
	if strings.Contains(out, "script") || strings.Contains(out, "alert") {
		t.Errorf("raw HTML leaked: %q", out)
	}
}

// TestFormatMessage_Markdown 验证 formatMessage 走 mdBody：正文渲染
// 后标记字符消失、内容保留，行前缀格式不变。
func TestFormatMessage_Markdown(t *testing.T) {
	h := newHistoryView(nil)
	line := formatMessage(&h, protocol.StoredMessage{
		ID: "m1", ServerSeq: 1, SenderUserID: "alice", Body: "**hi**", CreatedAt: 1700000000000,
	})
	if strings.Contains(line, "**") {
		t.Errorf("markers leaked into message line: %q", line)
	}
	if !strings.Contains(line, "[06:13:20] alice: ") {
		t.Errorf("prefix missing: %q", line)
	}
	if !strings.Contains(line, "hi") {
		t.Errorf("body missing: %q", line)
	}
}

// TestMdBody_CacheAndTruncate 验证 mdBody 的按 ID 缓存与先截断再渲染：
// 同一 ID 两次渲染结果一致；超长正文被截到 messageLineWrap。
func TestMdBody_CacheAndTruncate(t *testing.T) {
	h := newHistoryView(nil)
	long := strings.Repeat("x", messageLineWrap+50)
	m := protocol.StoredMessage{ID: "m1", Body: long}
	first := h.mdBody(m)
	second := h.mdBody(m)
	if first != second {
		t.Error("mdBody cache miss: same ID rendered twice differently")
	}
	if len(first) != messageLineWrap+3 { // 200 + "..."
		t.Errorf("truncated length = %d, want %d", len(first), messageLineWrap+3)
	}
	if !strings.HasSuffix(first, "...") {
		t.Errorf("truncation marker missing: %q", first[:8])
	}
}
