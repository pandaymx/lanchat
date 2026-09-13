// Command hub 是 lanchat 的独立 hub 入口（ADR-014 M-a 起退役为
// 调试 / 单机模式；默认形态是客户端内嵌 hub，见 pkg/hubserver）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pandaymx/lanchat/pkg/hubserver"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// --version 必须在 flag.Parse 之前识别：
	// flag 不认这个 flag，会先报"flag provided but not defined"再退出。
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("lanchat hub %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	addr := flag.String("addr", hubserver.DefaultAddr, "监听地址，例如 :9000 或 127.0.0.1:9000")
	path := flag.String("path", wstransportDefaultPath(), "WebSocket upgrade 路径")
	maxHistory := flag.Int("max-history", hubserver.DefaultMaxHistory, "单次 FKHistoryReq 补发的最大条数")
	// -db / -files 留空表示用平台默认数据目录（Windows %LOCALAPPDATA%、Linux
	// ~/.local/share、macOS ~/Library/Application Support），避免安装在
	// 只读目录（如 C:\Program Files\LAN Chat Hub）时无法写库和文件。
	dbPath := flag.String("db", "", "持久化库文件路径（libSQL/SQLite 格式）；填 memory 用纯内存不落盘；留空用平台默认数据目录")
	filesDir := flag.String("files", "", "文件传输（M9）的 blob 存储目录；文件存 <dir>/<FileID>；留空用平台默认数据目录")
	maxFileSize := flag.Int64("max-file-size", hubserver.DefaultMaxFileSize, "单文件上传上限（字节，默认 512MiB）；<=0 不限制")
	mDNS := flag.Bool("mdns", true, "通过 mDNS/DNS-SD 在局域网广播 hub（_lanchat._tcp）；-mdns=false 关闭")
	logLevel := flag.String("log-level", "info", "日志级别：debug|info|warn|error")
	logFormat := flag.String("log-format", "text", "日志格式：text|json")
	logFile := flag.String("log-file", "", "日志文件路径；空走 stderr")
	flag.Parse()

	// 解析日志 flag；不识别走默认 + stderr 警告，不中断启动。
	lvl, lvlErr := logging.ParseLevel(*logLevel)
	fmt2, fmtErr := logging.ParseFormat(*logFormat)
	if err := logging.Init(lvl, fmt2, *logFile); err != nil {
		fmt.Fprintln(os.Stderr, "hub: logging init:", err)
		os.Exit(1)
	}
	if lvlErr != nil {
		logging.New("hub").Warn("invalid -log-level, fallback to info", "input", *logLevel, "err", lvlErr)
	}
	if fmtErr != nil {
		logging.New("hub").Warn("invalid -log-format, fallback to text", "input", *logFormat, "err", fmtErr)
	}

	// signal.NotifyContext：SIGINT/SIGTERM 触发 ctx 取消 与 hub 关停联动。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := hubserver.Config{
		Addr:        *addr,
		Path:        *path,
		DBPath:      *dbPath,
		FilesDir:    *filesDir,
		MaxHistory:  *maxHistory,
		MaxFileSize: *maxFileSize,
		MDNS:        *mDNS,
		Version:     version,
	}
	srv, err := hubserver.Start(ctx, cfg)
	if err != nil {
		logging.New("hub").Error("hub start failed", "err", err)
		fmt.Fprintln(os.Stderr, "hub: start:", err)
		os.Exit(1)
	}
	logging.New("hub").Info("hub ready", "addr", srv.Addr(), "version", version, "commit", commit)

	if err := srv.Wait(); err != nil {
		logging.New("hub").Error("hub exited with error", "err", err)
	}
	logging.New("hub").Info("hub stopped gracefully")
}

// wstransportDefaultPath 取 WS 默认路径，避免 main 直接 import transport
// （路径常量由 hubserver 统一暴露更干净；这里通过 hubserver 获取）。
func wstransportDefaultPath() string { return hubserver.DefaultPath }
