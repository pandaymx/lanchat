// Command lanchat-daemon 是 LANChat 客户端常驻进程：
// 持有唯一的 core.Client，并通过本地 IPC（Unix socket / 命名管道）
// 以 JSON-RPC 2.0 向平台原生 UI 暴露 internal/appapi 契约。
//
// 生命周期由信号驱动：收到 SIGINT/SIGTERM 时关闭 IPC 与核心并退出。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/pandaymx/lanchat/internal/core"
	"github.com/pandaymx/lanchat/internal/ipc"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	log.SetFlags(log.LstdFlags)

	socket := flag.String("socket", defaultSocket(), "IPC 监听地址：Unix socket 路径（Linux/macOS）或命名管道名（Windows）")
	nickname := flag.String("nickname", "", "初始昵称（为空使用默认值）")
	downloadDir := flag.String("download-dir", "", "默认下载目录")
	showVersion := flag.Bool("version", false, "打印版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lanchat-daemon %s (commit %s, %s)\n", version, commit, runtime.GOOS)
		return
	}

	client := core.New(core.Options{
		Nickname:    *nickname,
		OS:          runtime.GOOS,
		DownloadDir: *downloadDir,
	})
	defer client.Close()

	srv := ipc.NewServer(client)
	client.SetListener(srv.Listener())

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe(*socket) }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("lanchat-daemon %s 启动：IPC %s", version, *socket)

	select {
	case err := <-errc:
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误:", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Printf("收到退出信号，正在关闭…")
		srv.Close()
		<-errc
	}
}

// defaultSocket 返回平台约定的默认 IPC 地址。
func defaultSocket() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\lanchat`
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir + "/lanchat.sock"
	}
	return "/tmp/lanchat.sock"
}
