//go:build desktop

// Command desktop 是 lanchat 的桌面端（Wails v3 窗口壳，ADR-015）：
//  1. 进程内起 webui（apps/desktop.Server，127.0.0.1 随机端口）
//  2. Wails 窗口 Navigate 到该本地地址，窗口内即完整 Web UI
//  3. 窗口关闭 → app.Run 返回 → Server.Close 释放端口与 hub 连接
//
// 本文件是 CGO 壳（import wails v3，系统 WebView），带 //go:build desktop
// tag 隔离：默认 go build ./... 不含本文件，CGO_ENABLED=0 交叉编译矩阵
// 不受影响；桌面端由 CI 原生 runner job 单独构建（见 AGENTS.md §6/§14）。
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/pandaymx/lanchat/apps/desktop/webapp"
	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// --version 必须在 flag.Parse 之前识别（与 cmd/hub|tui|web 同约定）。
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("lanchat desktop %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	hubURL := flag.String("hub-url", "", "hub 的 ws 地址；留空走 mDNS 自动发现")
	user := flag.String("user", "anonymous", "显示名（昵称即用）")
	convID := flag.String("conv", "lobby", "会话 ID；默认 lobby")
	logLevel := flag.String("log-level", "info", "日志级别：debug|info|warn|error")
	logFormat := flag.String("log-format", "text", "日志格式：text|json")
	logFile := flag.String("log-file", "", "日志文件路径；留空走 stderr")
	lang := flag.String("lang", "", "界面语言（en / zh-CN ...）；留空自动探测 $LC_ALL / $LANG / $LANGUAGE")
	langList := flag.Bool("lang-list", false, "列出已加载的 locale 并退出")
	flag.Parse()

	lvl, lvlErr := logging.ParseLevel(*logLevel)
	fmt2, fmtErr := logging.ParseFormat(*logFormat)
	if err := logging.Init(lvl, fmt2, *logFile); err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-desktop: logging init:", err)
		os.Exit(1)
	}
	if lvlErr != nil {
		logging.New("desktop").Warn("invalid -log-level, fallback to info", "input", *logLevel, "err", lvlErr)
	}
	if fmtErr != nil {
		logging.New("desktop").Warn("invalid -log-format, fallback to text", "input", *logFormat, "err", fmtErr)
	}

	// 启动期加载 i18n bundle（与 cmd/tui、cmd/web 同一套，照 AGENTS.md §13）。
	bundle := i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en")
	if *langList {
		locales := bundle.Locales()
		sort.Strings(locales)
		fmt.Printf("available locales: %v\n", locales)
		fmt.Printf("active fallback  : %s\n", "en")
		return
	}
	resolvedLocale := resolveLocale(*lang)
	logging.New("desktop").Info("i18n resolved", "locale", resolvedLocale)

	srv, err := webapp.Start(webapp.Options{
		HubURL:     *hubURL,
		User:       *user,
		ConvID:     *convID,
		Version:    version,
		Translator: bundle.ForLocale(resolvedLocale),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-desktop:", err)
		os.Exit(1)
	}
	// 窗口关闭（app.Run 返回）后释放本地服务：端口归还、hub 会话断开。
	defer func() { _ = srv.Close() }()

	app := application.New(application.Options{
		Name: "LAN Chat",
	})
	app.NewWebviewWindowWithOptions(application.WebviewWindowOptions{
		Title:  "LAN Chat",
		Width:  1024,
		Height: 700,
		URL:    srv.URL() + "/",
	})
	app.Run()
}

// resolveLocale 在 -lang 与 env 之间做优先级排序：flag > env > fallback
// （与 cmd/web 同语义，见 cmd/web/main.go 的注释）。
func resolveLocale(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return i18n.DetectLocale(os.Environ(), "en")
}
