package logging

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// resetDefault 在每个测试前后把 slog.Default 还原成 io.Discard handler，
// 避免一个测试改了 default 影响其他测试。
func resetDefault(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(newHandler(io.Discard, slog.LevelInfo, FormatText)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// TestParseLevel_AllKnown 覆盖所有合法级别 + 大小写不敏感。
func TestParseLevel_AllKnown(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" info ", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, tc := range cases {
		got, err := ParseLevel(tc.in)
		if err != nil {
			t.Fatalf("ParseLevel(%q) unexpected err: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestParseLevel_Unknown 返回 error 而不是 panic。
func TestParseLevel_Unknown(t *testing.T) {
	_, err := ParseLevel("trace")
	if err == nil {
		t.Fatal("ParseLevel(\"trace\") expected err, got nil")
	}
}

// TestParseFormat 覆盖 text/json 与大小写不敏感。
func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want Format
	}{
		{"text", FormatText},
		{"", FormatText},
		{"TXT", FormatText},
		{"json", FormatJSON},
		{"JSON", FormatJSON},
	}
	for _, tc := range cases {
		got, err := ParseFormat(tc.in)
		if err != nil {
			t.Fatalf("ParseFormat(%q) unexpected err: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseFormat(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	_, err := ParseFormat("xml")
	if err == nil {
		t.Fatal("ParseFormat(\"xml\") expected err, got nil")
	}
}

// TestNew_TextHandler 验证 ComponentLogger("hub") 在 text 模式下产生 component=hub 行。
func TestNew_TextHandler(t *testing.T) {
	resetDefault(t)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(newHandler(&buf, slog.LevelInfo, FormatText)))

	l := New("hub")
	l.Info("starting", "addr", ":9000")

	out := buf.String()
	if !strings.Contains(out, "component=hub") {
		t.Errorf("expected component=hub in output, got: %q", out)
	}
	if !strings.Contains(out, "msg=starting") {
		t.Errorf("expected msg=starting in output, got: %q", out)
	}
	if !strings.Contains(out, "addr=:9000") {
		t.Errorf("expected addr=:9000 in output, got: %q", out)
	}
}

// TestNew_JSONHandler 验证 ComponentLogger 在 json 模式下输出合法 JSON。
func TestNew_JSONHandler(t *testing.T) {
	resetDefault(t)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(newHandler(&buf, slog.LevelDebug, FormatJSON)))

	l := New("client")
	l.Debug("recv frame", "kind", "FKHello")

	out := buf.String()
	if !strings.Contains(out, `"component":"client"`) {
		t.Errorf("expected component:client in JSON output, got: %q", out)
	}
	if !strings.Contains(out, `"msg":"recv frame"`) {
		t.Errorf("expected msg:recv frame in JSON output, got: %q", out)
	}
}

// TestLevel_FilterOff Debug 级别在 Info 模式下不输出。
func TestLevel_FilterOff(t *testing.T) {
	resetDefault(t)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(newHandler(&buf, slog.LevelInfo, FormatText)))

	l := New("tui")
	l.Debug("should not appear")
	if buf.Len() != 0 {
		t.Errorf("expected no output at Debug, got: %q", buf.String())
	}
	l.Info("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Errorf("expected Info to appear, got: %q", buf.String())
	}
}

// TestDefault_NoInit 验证未 Init 时 slog.Default 不为 nil，
// 调用 New 不会 panic —— 单测与不在 main 调 Init 的场景能跑。
func TestDefault_NoInit(t *testing.T) {
	resetDefault(t)

	l := New("x")
	if l == nil {
		t.Fatal("New returned nil")
	}
	// 不 panic 即通过。
	_ = l
}

// TestComponentLogger_PicksUpNewDefault 是这个 PR 的核心回归测试：
// 包级 var 拿到的 ComponentLogger 必须在 slog.SetDefault 替换后用新 handler 写出。
//
// stdlib slog.Default().With(...) 会把 stdlib 默认 handler 引用固化，
// 这是 ComponentLogger 存在的根本理由。
func TestComponentLogger_PicksUpNewDefault(t *testing.T) {
	resetDefault(t)

	// 模拟 pkg/xxx 的包级 var：在 SetDefault 之前拿 logger
	l := New("pkg-x")

	// 模拟 main() 的 Init：替换全局 handler 为我们自定义的 text handler
	var buf bytes.Buffer
	slog.SetDefault(slog.New(newHandler(&buf, slog.LevelInfo, FormatText)))

	// 调 logger —— 必须用新的 handler
	l.Info("hello", "k", "v")

	out := buf.String()
	// 关键断言：msg 字段不应包含 "INFO" 字符串（stdlib 默认 handler 的 bug 特征）。
	if strings.Contains(out, "msg=\"INFO hello") {
		t.Errorf("msg field still contaminated with level string; output: %q", out)
	}
	// 正确输出格式：msg=hello component=pkg-x k=v
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("expected msg=hello in output, got: %q", out)
	}
	if !strings.Contains(out, "component=pkg-x") {
		t.Errorf("expected component=pkg-x, got: %q", out)
	}
}

// TestComponentLogger_AllLevels 覆盖四个级别，确保 method signature 都正确。
func TestComponentLogger_AllLevels(t *testing.T) {
	resetDefault(t)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(newHandler(&buf, slog.LevelDebug, FormatText)))

	l := New("all")
	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	out := buf.String()
	for _, level := range []string{"level=DEBUG msg=d", "level=INFO msg=i", "level=WARN msg=w", "level=ERROR msg=e"} {
		if !strings.Contains(out, level) {
			t.Errorf("missing %q in output: %q", level, out)
		}
	}
}

// TestName 验证 Name() 返回正确的 component。
func TestName(t *testing.T) {
	l := New("hello-world")
	if l.Name() != "hello-world" {
		t.Errorf("Name() = %q, want %q", l.Name(), "hello-world")
	}
}
