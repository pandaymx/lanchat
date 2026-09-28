// Command lanchat 是 LANChat 信令服务器入口。
//
// 子命令：
//
//	lanchat serve   [--config path] [--listen addr] ...   启动信令服务器
//	lanchat browse  [--timeout sec]                       浏览局域网内的服务器
//	lanchat genpsk                                        生成 PSK 的 bcrypt 哈希
//	lanchat version                                       打印版本信息
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "browse":
		err = runBrowse(os.Args[2:])
	case "genpsk":
		err = runGenPSK(os.Args[2:])
	case "version":
		runVersion()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `lanchat — LANChat 信令服务器

用法:
  lanchat serve   [--config path] [--listen addr] [--path p]
                  [--auth-mode psk|none] [--psk-hash hash]
                  [--heartbeat-sec n] [--idle-timeout-sec n] [--shutdown-grace-sec n]
                  [--relay-enabled] [--relay-listen addr] [--relay-host ip] [--relay-force]
  lanchat browse  [--timeout sec]
  lanchat genpsk
  lanchat version

配置优先级: CLI flag > 环境变量 LANCHAT_* > yaml 文件 > 默认值
`)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var configPath string
	fs.StringVar(&configPath, "config", "", "yaml 配置文件路径（可选）")
	var listen, path, authMode, pskHash string
	fs.StringVar(&listen, "listen", "", "监听地址，如 :19090")
	fs.StringVar(&path, "path", "", "WebSocket 路径，如 /lctp")
	fs.StringVar(&authMode, "auth-mode", "", "鉴权模式 psk|none")
	fs.StringVar(&pskHash, "psk-hash", "", "PSK 的 bcrypt 哈希")
	var heartbeatSec, idleTimeoutSec, shutdownGraceSec int
	fs.IntVar(&heartbeatSec, "heartbeat-sec", 0, "心跳间隔（秒）")
	fs.IntVar(&idleTimeoutSec, "idle-timeout-sec", 0, "空闲超时（秒）")
	fs.IntVar(&shutdownGraceSec, "shutdown-grace-sec", 0, "优雅关停宽限（秒）")
	var relayEnabled, relayForce bool
	fs.BoolVar(&relayEnabled, "relay-enabled", false, "启用回退中继数据面")
	var relayListen, relayHost string
	fs.StringVar(&relayListen, "relay-listen", "", "中继 TCP 监听地址，如 :19100")
	fs.StringVar(&relayHost, "relay-host", "", "宣告给客户端的中继主机（LAN IP）")
	fs.BoolVar(&relayForce, "relay-force", false, "强制走中继、跳过直连（测试/排障）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(configPath, explicitFlags(fs))
	if err != nil {
		return err
	}

	opts := server.Options{
		Listen:            cfg.Server.Listen,
		Path:              cfg.Server.Path,
		AuthMode:          cfg.Server.AuthMode,
		PSKHash:           cfg.Server.PSKHash,
		HeartbeatInterval: time.Duration(cfg.Server.HeartbeatSec) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeoutSec) * time.Second,
		ShutdownGrace:     time.Duration(cfg.Server.ShutdownGraceSec) * time.Second,
	}
	if cfg.Relay.Enabled {
		opts.RelayListen = cfg.Relay.Listen
		opts.RelayHost = cfg.Relay.Host
	}
	srv, err := server.New(opts, log.Printf)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("lanchat %s 启动：监听 %s，WS 路径 %s，鉴权 %s，协议 %s",
		version, opts.Listen, opts.Path, opts.AuthMode, protocol.ProtocolVersion)
	return srv.Serve(ctx)
}

// explicitFlags 从已解析的 FlagSet 提取用户显式给出的 flag（键为 config 包约定的 flagKey）。
func explicitFlags(fs *flag.FlagSet) map[string]string {
	out := make(map[string]string)
	fs.Visit(func(fl *flag.Flag) {
		out[fl.Name] = fl.Value.String()
	})
	return out
}

// runBrowse 在给定超时内通过 mDNS 浏览局域网中的 LANChat 服务器并打印列表。
func runBrowse(args []string) error {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	var timeoutSec int
	fs.IntVar(&timeoutSec, "timeout", 3, "浏览等待时长（秒）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if timeoutSec <= 0 {
		return fmt.Errorf("--timeout 必须为正数")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	browser := discover.NewBrowser()
	errc := make(chan error, 1)
	go func() { errc <- browser.Browse(ctx, nil) }()

	<-ctx.Done()
	servers := browser.Snapshot()
	// Browse 随超时结束返回；LookupType 此时回传 context 的 deadline 错误，
	// 对一次性浏览属正常收尾，不当作失败。
	if err := <-errc; err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("未在局域网内发现 LANChat 服务器")
		return nil
	}
	for _, s := range servers {
		fmt.Printf("%s  %s\n", orDefault(s.Name, s.ID), joinIPs(s.Addresses, s.Port))
		fmt.Printf("    id=%s path=%s auth=%s ver=%s tls=%t\n",
			s.ID, orDefault(s.Path, "/"), orDefault(s.Auth, "unknown"), s.Version, s.TLS)
	}
	return nil
}

// joinIPs 把地址列表与端口拼成 "192.168.1.47:19090, ..." 形式。
func joinIPs(ips []net.IP, port int) string {
	parts := make([]string, 0, len(ips))
	for _, ip := range ips {
		parts = append(parts, net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)))
	}
	return strings.Join(parts, ", ")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func runGenPSK(args []string) error {
	fs := flag.NewFlagSet("genpsk", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, "请输入 PSK 明文（输入不回显）: ")
	plain, err := readPassword()
	if err != nil {
		return err
	}
	if len(plain) == 0 {
		return fmt.Errorf("PSK 不能为空")
	}
	hash, err := config.HashPSK(string(plain))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	fmt.Println(hash)
	return nil
}

func runVersion() {
	fmt.Printf("lanchat %s (commit %s, protocol %s)\n", version, commit, protocol.ProtocolVersion)
}

// readPassword 读取一行 PSK 明文且不回显。
// stdin 为终端时关闭回显；非终端（管道/重定向）时按普通文本行读取。
// 全程无 CGO。
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return term.ReadPassword(fd)
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}
