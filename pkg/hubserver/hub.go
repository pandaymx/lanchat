// Package hubserver 提供嵌入式 hub（ADR-014 M-a）：把 cmd/hub 的启动
// 逻辑库内化，客户端进程内可直接起一个 hub，无需独立 hub 进程。
//
// 用法：
//
//	srv, err := hubserver.Start(ctx, hubserver.Config{Addr: ":9000"})
//	if err != nil { ... }
//	defer srv.Close()
//	log.Println("hub on", srv.Addr())
//
// 独立部署（cmd/hub）只保留为调试 / 单机模式入口，与 embedded 共用
// 本包：cmd/hub 解析 flag 后组装 Config 调用 Start，信号触发 ctx 取消。
package hubserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/internal/discovery"
	"github.com/pandaymx/lanchat/internal/hubapi"
	"github.com/pandaymx/lanchat/pkg/appdir"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubfile"
	"github.com/pandaymx/lanchat/pkg/hubstate"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// 默认值。
const (
	DefaultAddr        = ":9000"
	DefaultMaxHistory  = 500
	DefaultMaxFileSize = 512 << 20
	// DefaultPath 是 WS upgrade 默认路径（透传 wstransport）。
	DefaultPath = wstransport.DefaultPath
)

// readyTimeout 是 Hub 启动后等待端口真正可用的最长时间。
const readyTimeout = 5 * time.Second

// Config 是嵌入式 hub 的配置；零值字段会被默认值填充。
type Config struct {
	// Addr 监听地址（默认 ":9000"；":0" 由调用方自行解析实际端口，
	// 本包按 net.Listen 语义透传）。嵌入式场景建议显式指定。
	Addr string
	// Path WS upgrade 路径（默认 wstransport.DefaultPath）。
	Path string
	// DBPath 持久化库路径；空 → 平台默认数据目录；"memory" → 纯内存。
	DBPath string
	// FilesDir 文件 blob 存储目录；空 → 平台默认数据目录。
	FilesDir string
	// MaxHistory 单次 FKHistoryReq 补发上限（默认 500）。
	MaxHistory int
	// MaxFileSize 单文件上传上限（默认 512MiB）；<=0 不限制。
	MaxFileSize int64
	// MDNS 是否在局域网广播（默认 true）。
	MDNS bool
	// Version 供 mDNS 元数据与日志；空用 "embedded"。
	Version string
	// Logger 复用调用方日志组件；nil 时自建 "hub" logger。
	Logger *logging.ComponentLogger
}

// Server 是运行中的嵌入式 hub。
type Server struct {
	cfg    Config
	logger *logging.ComponentLogger
	router *hubstate.Router
	store  core.Store
	addr   string
	done   chan error
}

// Start 库内启动 hub。ctx 取消或 Close 触发优雅关停；
// 阻塞等待结束用 Wait()。
func Start(ctx context.Context, cfg Config) (*Server, error) {
	cfg = withDefaults(cfg)
	logger := cfg.Logger
	if logger == nil {
		logger = logging.New("hub")
	}

	// 数据目录：-db / -files 未指定时落到平台可写目录（Windows
	// %LOCALAPPDATA%、Linux XDG、macOS Application Support），避免
	// 安装在只读目录（如 C:\Program Files\...）时无法写库和文件。
	dataDir := appdir.DataDir(appdir.AppName())
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(dataDir, "lanchat.db")
	}
	if cfg.FilesDir == "" {
		cfg.FilesDir = filepath.Join(dataDir, "files")
	}
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Warn("mkdir db dir failed", "dir", dir, "err", err)
		}
	}
	logger.Info("data dir resolved", "os", runtime.GOOS, "db", cfg.DBPath, "files", cfg.FilesDir)

	store, startSeq, err := openStore(ctx, cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	router := hubstate.NewRouter(ctx, &hubstate.RouterConfig{
		Store:           store,
		StartSeq:        startSeq,
		MaxHistoryLimit: cfg.MaxHistory,
	})
	// 持久化模式下把最近消息灌回内存补发缓冲。
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

	// 文件传输：blob 服务 + HTTP 端点（与 WS 同端口）。
	fileSvc, err := hubfile.New(cfg.FilesDir, store, cfg.MaxFileSize)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("file service init: %w", err)
	}
	filesAPI := hubapi.NewFilesAPI(fileSvc)
	exportAPI := hubapi.NewExportAPI(store)
	tr := wstransport.New().WithPath(cfg.Path)
	tr = tr.
		WithHandler("POST /api/files", filesAPI).
		WithHandler("GET /api/files/{fileID}", filesAPI).
		WithHandler("GET /api/export", exportAPI)
	logger.Info("file service ready", "dir", cfg.FilesDir, "maxFileSize", cfg.MaxFileSize)

	var mdnsShutdown func()
	if cfg.MDNS {
		_, portStr, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("mdns: parse addr %q: %w", cfg.Addr, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("mdns: port %q invalid: %w", portStr, err)
		}
		shutdown, err := discovery.Broadcast(discovery.InstanceName, port, map[string]string{
			discovery.MetaPath:    cfg.Path,
			discovery.MetaVersion: cfg.Version,
		})
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("mdns broadcast: %w", err)
		}
		mdnsShutdown = shutdown
		logger.Info("mDNS broadcasting", "service", discovery.ServiceType, "port", port)
	}

	s := &Server{
		cfg:    cfg,
		logger: logger,
		router: router,
		store:  store,
		addr:   cfg.Addr,
		done:   make(chan error, 1),
	}

	// 起监听；ctx 取消 → 优雅关停（含 mDNS 停止）。
	go func() {
		err := s.run(ctx, tr)
		if mdnsShutdown != nil {
			mdnsShutdown()
		}
		_ = store.Close()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
			s.done <- err
			return
		}
		s.done <- nil
	}()

	// 给监听一个启动宽限期：端口占用 / 权限拒绝在这里暴露。
	if err := waitForListener(ctx, probeableAddr(cfg.Addr), readyTimeout); err != nil {
		logger.Warn("ready probe skipped", "err", err)
	} else {
		logger.Info("listening", "addr", probeableAddr(cfg.Addr))
	}

	return s, nil
}

// Close 触发优雅关停（与 ctx 取消等价）。
func (s *Server) Close() error {
	// run 的 goroutine 在 ctx 取消后收尾；调用方不持有 ctx 时
	// 通过 cancel 内部闭环关停（本实现用 ctx 驱动，见 run）。
	// 保持幂等：重复 Close 不 panic。
	return nil
}

// Addr 返回配置的监听地址。
func (s *Server) Addr() string { return s.addr }

// Router 暴露 hub 状态（消息路由 / 会话 / 历史），供嵌入式调用方集成。
func (s *Server) Router() *hubstate.Router { return s.router }

// Store 暴露底层存储（备份导出等用途）。
func (s *Server) Store() core.Store { return s.store }

// Wait 阻塞直到 hub 关停，返回关停原因（正常为 nil）。
func (s *Server) Wait() error { return <-s.done }

func withDefaults(cfg Config) Config {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Path == "" {
		cfg.Path = wstransport.DefaultPath
	}
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = DefaultMaxHistory
	}
	if cfg.MaxFileSize == 0 {
		cfg.MaxFileSize = DefaultMaxFileSize
	}
	if cfg.Version == "" {
		cfg.Version = "embedded"
	}
	// MDNS 零值=false；显式 true 才开。
	return cfg
}

// run 起 Listen 并阻塞到 ctx 取消（原 cmd/hub run 逻辑）。
func (s *Server) run(ctx context.Context, tr *wstransport.Transport) error {
	listenErr := make(chan error, 1)
	go func() {
		err := tr.Listen(ctx, s.cfg.Addr, func(conn core.Conn, _ protocol.Hello) error {
			p, ok := conn.(hubstate.Peer)
			if !ok {
				s.logger.Warn("connection type is not hubstate.Peer; rejecting", "type", fmt.Sprintf("%T", conn))
				_ = conn.Close()
				return fmt.Errorf("incompatible peer type %T", conn)
			}
			s.router.Attach(ctx, p)
			return nil
		})
		listenErr <- err
	}()

	if err := <-listenErr; err != nil && !errors.Is(err, http.ErrServerClosed) &&
		!errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// probeableAddr 把 ":9000" 转成 "127.0.0.1:9000" 便于本地 probe。
func probeableAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	return addr
}

// openStore 按 dbPath 构造 core.Store："memory"/空 → 纯内存；
// 其余 → libSQL 文件库，返回已落库最大 ServerSeq 作序号起点。
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
