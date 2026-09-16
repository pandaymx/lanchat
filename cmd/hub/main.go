// Command hub 是 lanchat 的独立 hub 入口（ADR-014 M-a 起退役为
// 调试 / 单机模式；默认形态是客户端内嵌 hub，见 pkg/hubserver）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pandaymx/lanchat/pkg/hubserver"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/mesh"
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
	// mesh：ADR-014 去中心化同步。需要持久化库（-db 不能是 memory）。
	meshMode := flag.Bool("mesh", false, "启用去中心化 mesh 同步（ADR-014 wire v2；需持久化库）")
	meshPeers := flag.String("peers", "", "mesh 邻居 base URL 列表，逗号分隔（如 http://192.168.1.5:9000,http://192.168.1.6:9000）")
	nodeID := flag.String("node-id", "", "本节点 mesh 标识（默认主机名；持久化后不可随意更改，消息坐标依赖它稳定）")
	joinToken := flag.String("join-token", "", "mesh 配对 Token：启用后未知节点必须携带该 Token 才被授权加入（gated TOFU）；空 = 保持旧 TOFU（首次见即信任）。配对 Token 用 -print-join-token 生成")
	printJoinToken := flag.Bool("print-join-token", false, "生成一个新的配对 Token 并打印，然后退出（供分享给要加入的新设备）")
	deviceName := flag.String("device", "", "本节点设备名（随 mesh 握手展示给邻居；默认主机名）")
	meshDir := flag.String("mesh-dir", "", "mesh 身份/信任表目录（默认与数据目录相同）；同机多实例时各自指定可避免身份互相覆盖")
	logLevel := flag.String("log-level", "info", "日志级别：debug|info|warn|error")
	logFormat := flag.String("log-format", "text", "日志格式：text|json")
	logFile := flag.String("log-file", "", "日志文件路径；空走 stderr")
	flag.Parse()

	// -print-join-token：生成配对 Token 即退出，不启动 hub。
	if *printJoinToken {
		tok, err := mesh.NewToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, "hub: generate join token:", err)
			os.Exit(1)
		}
		fmt.Println(tok)
		return
	}

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
		Mesh:        *meshMode,
		MeshPeers:   splitPeers(*meshPeers),
		NodeID:      *nodeID,
		JoinToken:   *joinToken,
		Device:      *deviceName,
		MeshDir:     *meshDir,
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

// splitPeers 把逗号分隔的邻居 URL 列表拆成 slice（去空白与空项）。
func splitPeers(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wstransportDefaultPath 取 WS 默认路径，避免 main 直接 import transport
// （路径常量由 hubserver 统一暴露更干净；这里通过 hubserver 获取）。
func wstransportDefaultPath() string { return hubserver.DefaultPath }
