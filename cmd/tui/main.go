// Command tui 是 lanchat 的终端客户端入口：
//  1. flag 取运行参数
//  2. tui.Dial 用 ws.Transport 连接 hub，建 Session 并完成握手
//  3. Session.Pump 在独立 goroutine 把 bus 事件投递到 m.Publish
//  4. tea.NewProgram 起 bubbletea 并阻塞运行，直到 quitMsg / Ctrl+C 退出
//  5. defer Session.Close 释放 sub/client/store
//
// 启动方式：
//
//	./bin/tui -user alice -hub ws://192.168.1.10:9000/ws
//
// 本文件不持有任何业务逻辑：状态、键位路由、渲染全在 pkg/tui.Model 里。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/internal/discovery"
	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/transport/ws"
	"github.com/pandaymx/lanchat/pkg/tui"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// unknownDevice 是取不到 hostname 时的兜底设备名。
const unknownDevice = "unknown"

// dialTimeout 是 lanchat-tui 启动时同步连 hub 的超时上限。
//
// bubbletea 在 p.Run() 之前必须拿到 Model，否则开 loop 后再加 Sender
// 就错过尺寸事件了；阻塞连接配合短超时是最直白的方案，
// 让用户在终端里立刻看到「hub 不可达」而不是 UI 傻等。
const dialTimeout = 5 * time.Second

// discoverTimeout 是 -hub 留空时 mDNS 浏览局域网的等待上限。
const discoverTimeout = 5 * time.Second

func main() {
	// --version 必须在 flag.Parse 之前识别：
	// flag 不认这个 flag，会先报"flag provided but not defined"再退出。
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("lanchat tui %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	user := flag.String("user", "", "显示名（昵称即用）")
	device := flag.String("device", "", "设备标识；留空取 hostname")
	hubURL := flag.String("hub", "", "hub 的 ws 地址，例如 ws://192.168.1.10:9000/ws")
	convID := flag.String("conv", "", "会话 ID；留空走 default (=lobby)")
	maxHist := flag.Int("max-hist", 0, "内存保留的最大消息条数；<=0 用默认 5000")
	noConnect := flag.Bool("no-connect", false, "跳过连接 hub（仅用于 UI 调试）")
	logLevel := flag.String("log-level", "info", "日志级别：debug|info|warn|error")
	logFormat := flag.String("log-format", "text", "日志格式：text|json")
	logFile := flag.String("log-file", defaultLogFile(), "日志文件路径；默认走 $TMPDIR/lanchat-tui-$$.log（TUI AltScreen 占用 stderr）")
	lang := flag.String("lang", "", "界面语言（en / zh-CN ...）；留空自动探测 $LC_ALL / $LANG / $LANGUAGE")
	langList := flag.Bool("lang-list", false, "列出已加载的 locale 并退出")
	flag.Parse()

	lvl, lvlErr := logging.ParseLevel(*logLevel)
	fmt2, fmtErr := logging.ParseFormat(*logFormat)
	if err := logging.Init(lvl, fmt2, *logFile); err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-tui: logging init:", err)
		os.Exit(1)
	}
	if lvlErr != nil {
		logging.New("tui").Warn("invalid -log-level, fallback to info", "input", *logLevel, "err", lvlErr)
	}
	if fmtErr != nil {
		logging.New("tui").Warn("invalid -log-format, fallback to text", "input", *logFormat, "err", fmtErr)
	}
	logger := logging.New("tui")
	logger.Info("starting tui", "version", version, "commit", commit, "user", *user, "device", *device, "hub", *hubURL, "log_file", *logFile)

	// 启动期加载 i18n bundle。命令行先于 env 探测，让用户在 CI / 容器
	// 里能用 -lang 强制覆盖 $LANG。
	bundle := i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en")
	if *langList {
		locales := bundle.Locales()
		sort.Strings(locales)
		fmt.Printf("available locales: %v\n", locales)
		fmt.Printf("active fallback  : %s\n", "en")
		return
	}
	resolvedLocale := resolveLocale(*lang)
	logger.Info("i18n resolved", "locale", resolvedLocale)

	if err := run(runOptions{
		User:       *user,
		Device:     *device,
		HubURL:     *hubURL,
		ConvID:     *convID,
		MaxHist:    *maxHist,
		NoConnect:  *noConnect,
		Translator: bundle.ForLocale(resolvedLocale),
	}); err != nil {
		logger.Error("tui exited with error", "err", err)
		fmt.Fprintln(os.Stderr, "lanchat-tui:", err)
		os.Exit(1)
	}
}

// defaultLogFile 给出 TUI 默认日志路径。
//
// bubbletea 的 AltScreen 会接管 stderr 并把光标定位、切到 alternate buffer；
// 在那之后写 stderr 的日志会被 bubbletea 的渲染覆盖或丢失。
// 因此 TUI 默认走 $TMPDIR/lanchat-tui-$$.log（按 PID 区分并发实例）。
//
// $TMPDIR 在 Linux/macOS 是合理路径；Windows 走 GetTempDir（缺省时退化为 ""）。
func defaultLogFile() string {
	dir := os.TempDir()
	if dir == "" {
		return ""
	}
	return fmt.Sprintf("%s%clanchat-tui-%d.log", dir, os.PathSeparator, os.Getpid())
}

// runOptions 收纳 run 的入参，避免签名再长一截。
type runOptions struct {
	User, Device, HubURL, ConvID string
	MaxHist                      int
	NoConnect                    bool
	Translator                   tui.Translator
}

// resolveLocale 在 -lang 与 env 之间做优先级排序：flag > env > fallback。
//
// env 部分直接走 i18n.DetectLocale（处理 $LC_ALL / $LANG / $LANGUAGE
// + BCP 47 简化的同名变体）。flag 显式给了空串就视为"未指定"，走 env。
//
// 注意：我们不在这里校验 flagValue 是否在已加载集合里——
// Bundle.T 对未知 locale 会自动走 fallback 链到 "en"，
// 保留用户输入的字面值便于日志排错。
func resolveLocale(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return i18n.DetectLocale(os.Environ(), "en")
}

// run 构造 Model、可选地建 Session、起 bubbletea Program。
func run(opts runOptions) error {
	if opts.Device == "" {
		opts.Device = defaultDeviceName()
	}

	cfg := tui.Config{
		User:       opts.User,
		Device:     opts.Device,
		HubURL:     opts.HubURL,
		MaxHist:    opts.MaxHist,
		Translator: opts.Translator,
	}
	m := tui.New(cfg)

	var pumpCancel context.CancelFunc
	if !opts.NoConnect {
		session, pumpCtx, cancel, err := dialSession(opts)
		if err != nil {
			return err
		}
		pumpCancel = cancel
		defer func() { _ = session.Close() }()

		m.AttachSender(session)
		m.AttachConversations(session)
		// M9：文件收发能力。Session 同时实现 FileSender/FileReceiver；
		// 不注入时 /file 报「不可用」、他人附件不自动下载。
		m.SetFileTransfer(session, session)
		go func() {
			session.Pump(pumpCtx, m.Publish)
		}()
	}

	// tea.NewProgram 不传 option：alt screen 由 Model.View() 里的
	// v.AltScreen 控制（bubbletea v2 把该行为从 Program option 移到了 View）。
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if pumpCancel != nil {
		pumpCancel()
	}
	if err != nil {
		return fmt.Errorf("run tui program: %w", err)
	}
	_ = finalModel
	return nil
}

// dialSession 完成 Dial 并返回带 cancel 的 pumpCtx 给调用方。
//
// 取出 pumpCtx 与 cancel 是因为 Pump 必须与 bubbletea Program 同寿命：
// Run 返回后我们调 cancel 让 Pump 的 select 尽快退出，否则 Pump 会
// 拿着已经 Close 过的 sub.C() 报错。
func dialSession(opts runOptions) (*tui.Session, context.Context, context.CancelFunc, error) {
	if opts.HubURL == "" {
		// -hub 留空：走 mDNS 自动发现局域网内的 hub（M6.1）。
		discoverCtx, discoverCancel := context.WithTimeout(context.Background(), discoverTimeout)
		found, err := discovery.ResolveHubURL(discoverCtx, discoverTimeout)
		discoverCancel()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("未指定 -hub 且自动发现失败: %w", err)
		}
		opts.HubURL = found
		fmt.Fprintln(os.Stderr, "lanchat: 自动发现 hub:", found)
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), dialTimeout)
	defer dialCancel()

	session, err := tui.Dial(dialCtx, tui.DialOptions{
		Transport:    &ws.Transport{},
		HubURL:       opts.HubURL,
		User:         opts.User,
		Device:       opts.Device,
		ConvID:       opts.ConvID,
		HistoryLimit: opts.MaxHist,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	return session, pumpCtx, pumpCancel, nil
}

// defaultDeviceName 取 hostname 作为默认设备标识；取不到就退回 "unknown"。
//
// 多设备同步要求 device 在同一 user 下唯一（见方案 ADR-008）。hostname 在
// 局域网里通常够用；重名时用 -device 显式指定。
func defaultDeviceName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return unknownDevice
	}
	return h
}
