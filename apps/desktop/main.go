//go:build desktop

// Command desktop 是 lanchat 的桌面端（Wails v3 窗口壳，ADR-015）：
//  1. 进程内起 webui（apps/desktop/webapp.Server，127.0.0.1 随机端口）
//  2. Wails 窗口 Navigate 到该本地地址，窗口内即完整 Web UI
//  3. 系统托盘（M10.2）：常驻图标 + 菜单（打开窗口 / 退出）
//  4. 新消息系统通知（M10.2）：webui.Manager.OnMessage 钩子 → 平台通知
//     （linux notify-send / darwin osascript / windows PowerShell balloon）
//  5. 窗口关闭 → app.Run 返回 → Server.Close 释放端口与 hub 连接
//
// 本文件是 CGO 壳（import wails v3，系统 WebView），带 //go:build desktop
// tag 隔离：默认 go build ./... 不含本文件，CGO_ENABLED=0 交叉编译矩阵
// 不受影响；桌面端由 CI 原生 runner job 单独构建（见 AGENTS.md §6/§14）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/pandaymx/lanchat/apps/desktop/webapp"
	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// notifyCh 是 webui 事件泵 → 通知 goroutine 的桥：OnMessage 回调只做
// 非阻塞投递（事件泵要求快速返回），通知的过滤/限流/平台命令都在
// notificationLoop 里。
var notifyCh = make(chan *protocol.StoredMessage, 64)

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
	tr := bundle.ForLocale(resolvedLocale)
	logging.New("desktop").Info("i18n resolved", "locale", resolvedLocale)

	srv, err := webapp.Start(webapp.Options{
		HubURL:     *hubURL,
		User:       *user,
		ConvID:     *convID,
		Version:    version,
		Translator: tr,
		// 只投递，不做事：事件泵同步调用必须快返回。
		OnMessage: func(msg *protocol.StoredMessage) {
			select {
			case notifyCh <- msg:
			default: // 队列满：丢弃（通知是尽力而为，不能拖垮 UI）
			}
		},
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
	// beta.17：窗口创建是包级函数 NewWindow（App 上无 NewWebviewWindow*
	// 方法）；创建的窗口即注册为默认窗口（托盘 ShowWindow 的目标）。
	application.NewWindow(application.WebviewWindowOptions{
		Title:  "LAN Chat",
		Width:  1024,
		Height: 700,
		URL:    srv.URL() + "/",
	})
	setupTray(app, tr)
	go notificationLoop(*user)
	app.Run()
}

// setupTray 配置系统托盘（M10.2）：图标 + 菜单（打开窗口 / 退出）。
// 托盘常驻让关窗不退出成为可能——窗口关闭只隐藏，进程仍在（托盘退出）。
func setupTray(app *application.App, tr i18n.Translator) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIconPNG())
	menu := application.NewMenu()
	show := menu.Add(tr.T("desktop.tray.show"))
	show.OnClick(func(*application.Context) { tray.ShowWindow() })
	menu.AddSeparator()
	quit := menu.Add(tr.T("desktop.tray.quit"))
	quit.OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.Run()
}

// notificationLoop 消费 webui 事件泵投递的新消息并弹系统通知：
//   - 自己发的（回环）不通知；
//   - 同一发送者 1 秒内的连发合并为一条（限流）；
//   - 通知是尽力而为：平台命令失败只记日志。
func notificationLoop(self string) {
	last := make(map[string]time.Time)
	for msg := range notifyCh {
		if msg == nil || msg.SenderUserID == "" || msg.SenderUserID == self {
			continue
		}
		if t, ok := last[msg.SenderUserID]; ok && time.Since(t) < time.Second {
			continue
		}
		last[msg.SenderUserID] = time.Now()
		title := msg.SenderUserID + " \u00b7 LAN Chat"
		body := preview(msg.Body, 80)
		logging.New("desktop").Debug("new message notification", "from", msg.User)
		go platformNotify(title, body)
	}
}

// preview 把消息体折叠成单行并截断（通知栏只显示一行）。
func preview(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// platformNotify 按平台弹系统通知。无平台命令可用时静默失败
// （LAN Chat 是工具，通知不应成为硬依赖）。
func platformNotify(title, body string) {
	switch runtime.GOOS {
	case "linux":
		// libnotify：主流桌面（GNOME/KDE/XFCE）都带。
		if err := exec.Command("notify-send", "-a", "LAN Chat", "-i", "dialog-information", title, body).Run(); err != nil {
			logging.New("desktop").Debug("notify-send failed", "err", err)
		}
	case "darwin":
		// osascript display notification：无需开发者证书即可弹（UNUser
		// NotificationCenter 会显示来源为 Script Editor，可接受）。
		script := fmt.Sprintf("display notification %s with title %s", appleQuote(body), appleQuote(title))
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			logging.New("desktop").Debug("osascript notification failed", "err", err)
		}
	case "windows":
		// WinForms NotifyIcon 气泡：无需 AppID/签名（toast 受限），
		// 展示后 Sleep 再 Dispose，避免图标残留托盘。
		script := fmt.Sprintf(
			"Add-Type -AssemblyName System.Windows.Forms; $n=New-Object System.Windows.Forms.NotifyIcon; $n.Icon=[System.Drawing.SystemIcons]::Information; $n.BalloonTipTitle=%s; $n.BalloonTipText=%s; $n.Visible=$true; $n.ShowBalloonTip(4000); Start-Sleep -Milliseconds 6000; $n.Dispose()",
			psQuote(title), psQuote(body))
		if err := exec.Command("powershell", "-NoProfile", "-Command", script).Run(); err != nil {
			logging.New("desktop").Debug("balloon notification failed", "err", err)
		}
	}
}

// appleQuote 把字符串包进 AppleScript 双引号并转义（osascript 的
// display notification 参数是 "..." 字符串字面量，内部 \" 转义）。
func appleQuote(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(s)
	return "\"" + r + "\""
}

// psQuote 把字符串包进 PowerShell 单引号并转义（单引号内 ” 表示一个 '）。
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// trayIconPNG 程序化生成 32x32 托盘图标：品牌蓝圆底 + 白色聊天气泡
// + 三个省略号点（无字体依赖，标准库 image/png 即可）。后续有正式
// logo 设计时替换。
func trayIconPNG() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	blue := color.RGBA{R: 0x2f, G: 0x6f, B: 0xed, A: 0xff}
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	none := color.RGBA{}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x-15.5), float64(y-15.5)
			if dx*dx+dy*dy <= 14.5*14.5 {
				img.SetRGBA(x, y, blue)
			} else {
				img.SetRGBA(x, y, none)
			}
		}
	}
	roundRect(img, 7, 9, 25, 22, 3, white)
	for _, px := range []int{13, 16, 19} {
		dot(img, px, 15, 1.6, blue)
	}
	// 气泡尾巴（左下角小三角）
	for i := 0; i < 4; i++ {
		for j := 0; j <= i; j++ {
			img.SetRGBA(9+i, 23+j, white)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func roundRect(img *image.RGBA, x0, y0, x1, y1, r int, c color.RGBA) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			// 圆角近似：四角外半径 r 内的点排除
			in := true
			for _, c2 := range [][2]int{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
				if x < c2[0]+r && x > c2[0]-r && y < c2[1]+r && y > c2[1]-r {
					dx, dy := float64(x-c2[0]), float64(y-c2[1])
					if dx*dx+dy*dy > float64(r*r) {
						in = false
					}
				}
			}
			if in {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func dot(img *image.RGBA, cx, cy int, r float64, c color.RGBA) {
	for y := cy - 2; y <= cy+2; y++ {
		for x := cx - 2; x <= cx+2; x++ {
			dx, dy := float64(x-cx), float64(y-cy)
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// resolveLocale 在 -lang 与 env 之间做优先级排序：flag > env > fallback
// （与 cmd/web 同语义，见 cmd/web/main.go 的注释）。
func resolveLocale(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return i18n.DetectLocale(os.Environ(), "en")
}
