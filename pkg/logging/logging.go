// Package logging 是 lanchat 进程的日志门面。
//
// 选型：
//   - 用 log/slog（Go 1.21+ 标准库）而不是 zap/zerolog，
//     避免引入新的 transitive 依赖，与本仓库零依赖策略一致。
//   - 性能对本仓库够用（Hub 不是高 QPS 服务，TUI 是单机交互）。
//
// 用法（cmd 层只做一次 Init，包内只读）：
//
//	func main() {
//	    logging.Init(slog.LevelInfo, logging.FormatText, "") // stderr
//	    ...
//	}
//
//	// pkg 内
//	var pkgLog = logging.New("pkg-name")
//	pkgLog.Info("connected", "peer", peer)
//
// 设计取舍：
//   - Init 调 slog.SetDefault 全局注入，避免每个包都传 logger 参数；
//     单测如需隔离可以 slog.SetDefault 替换。
//   - 没调 Init 时 slog.Default 是 stderr + Info 兜底，包内也能直接用。
//   - 不暴露 With() 之类的链式 API：包级一次性带 component= 足够。
//
// 限制（M1 阶段不引入）：
//   - 不做 log file 滚动（lumberjack 等）；
//     留给 M5 部署阶段或系统 logrotate。
//   - 不接 OpenTelemetry；trace/span 字段留接口位（WithCtx）。
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format 决定 handler 的输出风格。ParseFormat 把字符串转 Format。
type Format string

const (
	// FormatText 是 slog.NewTextHandler 的格式，本地开发友好。
	FormatText Format = "text"
	// FormatJSON 是 slog.NewJSONHandler 的格式，生产给日志聚合系统用。
	FormatJSON Format = "json"
)

// ParseFormat 把字符串归一为 Format；不识别走 text 并返回 error。
//
// 故意不静默 fallback —— 调用方应明确知道格式没生效。
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text", "txt":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	default:
		return FormatText, fmt.Errorf("logging: unknown format %q (want text|json)", s)
	}
}

// ParseLevel 把字符串归一为 slog.Level；不识别走 info 并返回 error。
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("logging: unknown level %q (want debug|info|warn|error)", s)
	}
}

// newHandler 构造 (level, format) 组合的 slog.Handler。
// 解耦 Init 与文件管理，便于单测用 bytes.Buffer 注入。
func newHandler(w io.Writer, level slog.Level, format Format) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	switch format {
	case FormatJSON:
		return slog.NewJSONHandler(w, opts)
	default:
		return slog.NewTextHandler(w, opts)
	}
}

// Init 进程级初始化：构造 logger 并 slog.SetDefault 全局注入。
//
// file 为空 → 输出到 stderr；
// file 非空 → 追加写（O_CREATE|O_APPEND|O_WRONLY），权限 0644；
//
//	不关闭已打开的文件，调用方决定生命周期。
//	多次 Init 会替换 SetDefault 的 logger（用于单测或运行时切换）。
//
// Init 返回 error 时不修改 slog.Default，保证旧的 logger 继续生效。
func Init(level slog.Level, format Format, file string) error {
	var w io.Writer = os.Stderr
	if file != "" {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("logging: open log file %q: %w", file, err)
		}
		w = f
	}
	slog.SetDefault(slog.New(newHandler(w, level, format)))
	return nil
}

// Default 返回 slog.Default；Init 未调时 slog.Default 是 stderr + Info 兜底。
func Default() *slog.Logger { return slog.Default() }

// ComponentLogger 是带 component= attr 的轻量 logger。
//
// 设计要点：每次调用走**当前** slog.Default()，不缓存派生 logger。
// 这样 `var xxx = logging.New("x")` 包级 init 时拿到的空壳，
// 在 main() 里 slog.SetDefault(...) 替换全局 handler 后，
// 后续 xxx.Info(...) 调用会自然用新 handler —— 不会像
// slog.Default().With(...) 那样把 stdlib 默认 handler 引用固化。
//
// 复现 bug：见 git log 「fix: logging 包级 init 固化 stdlib 默认 handler」；
// 现象是 msg 字段被错误地 format 进了 level 字符串。
//
// 性能：每次调用走 slog.Default().Log + append args，比 slog.Default().With(...) 派生
// 略贵（多一次 slice append），对非高频路径无影响。
type ComponentLogger struct {
	name string
}

// New 给 pkg 内部用，返带 component= 前缀的 logger。
//
// component 是稳定的字符串标识（pkg 名或子模块名），不要带空格。
func New(component string) *ComponentLogger { return &ComponentLogger{name: component} }

// Name 返回 logger 的 component 名（便于诊断/logging self）。
func (l *ComponentLogger) Name() string { return l.name }

// log 把 message + args + component 一起传给当前 slog.Default。
//
// component= 放在最前面，便于 grep 与日志聚合系统按 component 分桶。
// slog 要求 args 偶数位 string key，否则当 format 参数用。
//
// 用 context.Background 是有意的：ComponentLogger 是 pkg 级 logger，不与具体
// 调用方 ctx 绑定；trace/span 等字段未来若需要，可在 args 里手动加，不应
// 把任意 ctx 里的 deadline 灌到日志里（会让调用方困惑）。
func (l *ComponentLogger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	full := make([]any, 0, len(args)+2)
	full = append(full, "component", l.name)
	full = append(full, args...)
	slog.Default().Log(ctx, level, msg, full...)
}

// Debug 按 Debug 级别记录；args 是 key-value pair 序列。
//
// 接受 ctx 主要是与 slog.Logger 签名对齐，但 ComponentLogger 内部用 Background
// （见 log 注释）；调用方无需关注 ctx 传递。
//
//nolint:contextcheck // ComponentLogger 故意不带 ctx 透传，见 PR 注释。
func (l *ComponentLogger) Debug(msg string, args ...any) {
	l.log(context.Background(), slog.LevelDebug, msg, args...)
}

// Info 按 Info 级别记录。
//
//nolint:contextcheck // ComponentLogger 故意不带 ctx 透传，见 PR 注释。
func (l *ComponentLogger) Info(msg string, args ...any) {
	l.log(context.Background(), slog.LevelInfo, msg, args...)
}

// Warn 按 Warn 级别记录。
//
//nolint:contextcheck // ComponentLogger 故意不带 ctx 透传，见 PR 注释。
func (l *ComponentLogger) Warn(msg string, args ...any) {
	l.log(context.Background(), slog.LevelWarn, msg, args...)
}

// Error 按 Error 级别记录。
//
//nolint:contextcheck // ComponentLogger 故意不带 ctx 透传，见 PR 注释。
func (l *ComponentLogger) Error(msg string, args ...any) {
	l.log(context.Background(), slog.LevelError, msg, args...)
}
