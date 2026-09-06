// Command web 是 lanchat 的浏览器端入口：
//  1. flag 取运行参数
//  2. logging.Init 接 slog（与 cmd/hub / cmd/tui 同风格）
//  3. 构造 internal/webui.Handler 并挂路由
//  4. http.Server 起在 -addr，signal.NotifyContext 做优雅退出
//
// 启动方式（先跑 hub，再跑 web）：
//
//	./bin/hub -addr :9000
//	./bin/web -addr :9001 -hub-url ws://127.0.0.1:9000/ws -user alice
//
// 本文件不持有任何业务逻辑：路由在 internal/webui.Handler，
// M4.3 起业务（Session / SSE fan-out）也在 internal/webui。
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pandaymx/lanchat/internal/webui"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// 版本号由构建注入，见 Makefile LDFLAGS。不要在这里写死版本号。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// shutdownTimeout 是优雅退出的等待上限。
//
// 超过这个时间还没退完就直接关——SSE 长连接理论上不会主动断，
// 所以 Shutdown 会一直等；给个上限防止运维时卡住。
const shutdownTimeout = 5 * time.Second

func main() {
	// --version 必须在 flag.Parse 之前识别：
	// flag 不认这个 flag，会先报"flag provided but not defined"再退出。
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("lanchat web %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	addr := flag.String("addr", ":9001", "HTTP 监听地址（:9001 或 127.0.0.1:9001）")
	hubURL := flag.String("hub-url", "", "hub 的 ws 地址；M4.2 骨架阶段暂不使用，M4.3 接通")
	user := flag.String("user", "anonymous", "显示名（昵称即用）")
	device := flag.String("device", "", "设备标识；留空自动生成 web-<random>")
	convID := flag.String("conv", "lobby", "会话 ID；默认 lobby")
	logLevel := flag.String("log-level", "info", "日志级别：debug|info|warn|error")
	logFormat := flag.String("log-format", "text", "日志格式：text|json")
	logFile := flag.String("log-file", "", "日志文件路径；留空走 stderr")
	flag.Parse()

	lvl, lvlErr := logging.ParseLevel(*logLevel)
	fmt2, fmtErr := logging.ParseFormat(*logFormat)
	if err := logging.Init(lvl, fmt2, *logFile); err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-web: logging init:", err)
		os.Exit(1)
	}
	if lvlErr != nil {
		logging.New("web").Warn("invalid -log-level, fallback to info", "input", *logLevel, "err", lvlErr)
	}
	if fmtErr != nil {
		logging.New("web").Warn("invalid -log-format, fallback to text", "input", *logFormat, "err", fmtErr)
	}

	if err := run(runOptions{
		Addr:    *addr,
		HubURL:  *hubURL,
		User:    *user,
		Device:  *device,
		ConvID:  *convID,
		Version: version,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "lanchat-web:", err)
		os.Exit(1)
	}
}

// runOptions 收纳 run 的入参，避免签名再长一截。
type runOptions struct {
	Addr    string
	HubURL  string // M4.2 未使用；M4.3 起传给 Session
	User    string
	Device  string
	ConvID  string
	Version string
}

// run 起 HTTP 服务并阻塞到收到退出信号。
func run(opts runOptions) error {
	logger := logging.New("web")
	logger.Info("starting web", "version", version, "commit", commit, "addr", opts.Addr, "user", opts.User)

	if opts.Device == "" {
		opts.Device = defaultDeviceName()
	}

	mux := http.NewServeMux()
	h := webui.NewHandler(webui.Config{
		User:    opts.User,
		Device:  opts.Device,
		ConvID:  opts.ConvID,
		Version: opts.Version,
	})
	h.Routes(mux)

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// signal.NotifyContext：Ctrl+C / SIGTERM 时取消 ctx，触发 Shutdown。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", opts.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	logger.Info("web stopped")
	return nil
}

// defaultDeviceName 生成 web 端默认设备标识。
//
// 格式 web-<8 位随机十六进制>：每次进程启动都不同，符合 ADR-008
// （同 user 多设备各自独立 ReadCursor）。M4.4 起会改成按 cookie 稳定下来。
func defaultDeviceName() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 读失败意味着系统熵源不可用，属于严重环境问题；
		// 这里不 panic 而是退回固定串，避免 web server 起不来。
		return "web-unknown"
	}
	return fmt.Sprintf("web-%x", b)
}
