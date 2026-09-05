// Command tui 是 lanchat 的终端客户端入口：起 bubbletea Program，
// 把键盘事件交给 pkg/tui.Model.Update 路由。
//
// 启动方式：
//
//	./bin/tui -user alice -hub ws://192.168.1.10:9000/ws
//
// 实现分层：
//   - flag 取运行参数（user / device / hub / max-hist）
//   - tui.New 构造 Model（Model 不持有 client；外部事件经 inbox 投递）
//   - tea.NewProgram 起程序并阻塞到退出
//
// 本文件不持有任何业务逻辑：状态、键位路由、渲染全在 pkg/tui.Model 里。
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

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
	maxHist := flag.Int("max-hist", 0, "内存保留的最大消息条数；<=0 用默认 5000")
	flag.Parse()

	if err := run(*user, *device, *hubURL, *maxHist); err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-tui:", err)
		os.Exit(1)
	}
}

// run 构造 Model 并阻塞运行 bubbletea Program。
//
// M3.4 只起 UI 壳：client 的连接与事件总线在 M3.5 接入
// （client.Events() → m.Publish、submitMsg → core.Send）。
// 因此此时 -hub 只作展示用途，不建立连接。
func run(user, device, hubURL string, maxHist int) error {
	if device == "" {
		device = defaultDeviceName()
	}

	m := tui.New(tui.Config{
		User:    user,
		Device:  device,
		HubURL:  hubURL,
		MaxHist: maxHist,
	})

	// tea.NewProgram 不传 option：alt screen 由 Model.View() 里的
	// v.AltScreen 控制（bubbletea v2 把该行为从 Program option 移到了 View）。
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run tui program: %w", err)
	}
	return nil
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
