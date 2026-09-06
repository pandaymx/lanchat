// Command hub 是 lanchat 的服务端入口：监听 WebSocket、按协议路由消息、维护在线状态与多设备投递。
//
// 启动方式：
//
//	./bin/hub -addr :9000 -path /ws
//
// 实现分层：
//   - flag 取运行参数
//   - -db 选 Store：默认 libSQL 文件库（M5，ADR-013，纯 Go 零 CGO），
//     -db memory 退回纯内存
//   - hubstate.Router 协议状态机（与 fake Hub 共用，验证抽象成立）
//   - ws.Transport 传输层（coder/websocket）
//
// 本文件不持有任何业务逻辑：所有协议语义都在 hubstate.Router 里。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubstate"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// readyTimeout 是 Hub 启动后等待端口真正可用的最长时间。
// 超过这个时间还没收到信号就当作启动失败，避免端口占用等问题被吞掉。
const readyTimeout = 5 * time.Second

func main() {
	// --version 必须在 flag.Parse 之前识别：
	// flag 不认这个 flag，会先报"flag provided but not defined"再退出。
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("lanchat hub %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	addr := flag.String("addr", ":9000", "监听地址，例如 :9000 或 127.0.0.1:9000")
	path := flag.String("path", wstransport.DefaultPath, "WebSocket upgrade 路径")
	maxHistory := flag.Int("max-history", 500, "单次 FKHistoryReq 补发的最大条数")
	dbPath := flag.String("db", "lanchat.db", "持久化库文件路径（libSQL/SQLite 格式）；填 memory 用纯内存不落盘")
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
	logger := logging.New("hub")

	// signal.NotifyContext：SIGINT/SIGTERM 触发 ctx 取消 与 run 关停联动。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, startSeq, err := openStore(ctx, *dbPath)
	if err != nil {
		logger.Error("open store failed", "err", err)
		fmt.Fprintln(os.Stderr, "hub: open store:", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	router := hubstate.NewRouter(&hubstate.RouterConfig{
		Store:           store,
		StartSeq:        startSeq,
		MaxHistoryLimit: *maxHistory,
	})
	// 持久化模式下把最近消息灌回内存补发缓冲：hist 有界（每会话 5000 条），
	// 重启后客户端的离线补发仍由内存路径服务（router.handleHistoryReq）。
	if ls, ok := store.(*libsql.Store); ok {
		recent, err := ls.RecentMessages(ctx, hubstate.HistoryRestoreLimit)
		if err != nil {
			logger.Error("restore history buffer failed", "err", err)
		} else {
			for i := range recent {
				router.History().Append(recent[i])
			}
			logger.Info("restored history buffer", "messages", len(recent), "startSeq", startSeq)
		}
	}
	tr := wstransport.New().WithPath(*path)

	logger.Info("starting hub", "version", version, "commit", commit, "addr", *addr, "path", *path, "db", *dbPath)

	if err := run(ctx, logger, tr, router, *addr); err != nil &&
		!errors.Is(err, context.Canceled) {
		logger.Error("hub exited with error", "err", err)
	}
	logger.Info("hub stopped gracefully")
}

// run 起 Listen 并阻塞到 ctx 取消。
//
// Listen 内部起 HTTP 服务；ctx 取消后 Shutdown 让 Serve 返回 http.ErrServerClosed，
// 我们把这个当成正常退出码（不视为错误）。
func run(
	ctx context.Context,
	logger *logging.ComponentLogger,
	tr *wstransport.Transport,
	router *hubstate.Router,
	addr string,
) error {
	// Listen 的回调：把每条 ws 连接交给 Router.Attach，
	// 同步登记后立刻返回，Attach 内部起的 goroutine 跑读循环。
	listenErr := make(chan error, 1)
	go func() {
		err := tr.Listen(ctx, addr, func(conn core.Conn, _ protocol.Hello) error {
			// ws.conn 同时实现 core.Conn 与 hubstate.Peer（见 pkg/transport/ws/conn.go）。
			// 这里直接断言；不是 Peer 类型的连接我们关掉。
			p, ok := conn.(hubstate.Peer)
			if !ok {
				logger.Warn("connection type is not hubstate.Peer; rejecting", "type", fmt.Sprintf("%T", conn))
				_ = conn.Close()
				return fmt.Errorf("incompatible peer type %T", conn)
			}
			router.Attach(ctx, p)
			return nil
		})
		listenErr <- err
	}()

	// 给 Listen 一个启动宽限期。http.Server 没法直接报告"已绑端口"，
	// 我们用一个轻量的 TCP probe 探测端口来确认服务真的起来了。
	probeAddr := probeableAddr(addr)
	if err := waitForListener(ctx, probeAddr, readyTimeout); err != nil {
		// probe 失败不一定要 fatal：Listen 自己会通过 listenErr 报错；
		// 这里只是日志提示，不阻塞等待。
		logger.Warn("ready probe skipped", "err", err)
	} else {
		logger.Info("listening", "addr", probeAddr)
	}

	if err := <-listenErr; err != nil && !errors.Is(err, http.ErrServerClosed) &&
		!errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// probeableAddr 把 ":9000" 这类裸端口写法转成 "127.0.0.1:9000"，便于本地 probe。
// "127.0.0.1:9000" / "0.0.0.0:9000" 原样返回。
func probeableAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	return addr
}

// openStore 按 -db 参数构造 core.Store：
//   - "memory" 或空串：纯内存 Store（memory），进程退出数据即丢；
//   - 其余：libSQL 文件库（ADR-013，纯 Go 驱动零 CGO），Open 时幂等
//     建表；返回的 startSeq 是已落库最大 ServerSeq，作为 Router 重启后
//     的序号起点，避免新消息与历史撞号。
func openStore(ctx context.Context, dbPath string) (core.Store, uint64, error) {
	if dbPath == "" || dbPath == "memory" {
		return memory.New(), 0, nil
	}
	dsn := dbPath
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	s, err := libsql.Open(ctx, dsn)
	if err != nil {
		return nil, 0, err
	}
	startSeq, err := s.MaxSeq(ctx)
	if err != nil {
		_ = s.Close()
		return nil, 0, err
	}
	return s, startSeq, nil
}

// waitForListener 用 TCP Dial 探测端口直到连通或超时。
//
// 注意：这只是"端口已 bind"的弱证明（不能证明 WS handler 就绪），
// 但比直接信 Listen 不报错要好——端口占用、权限拒绝都能在这步暴露。
func waitForListener(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
