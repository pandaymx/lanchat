// Package webapp 是 lanchat 桌面端的本地 Web 装配：进程内起 webui
// （127.0.0.1 随机端口），由 Wails 窗口加载。纯 Go 无 CGO，可本地编译与
// 单测；CGO 壳（Wails 窗口）在 apps/desktop/main.go，带 //go:build desktop
// tag 隔离（ADR-015）。
//
// 与 cmd/web 的关系：复用同一套 webui.Manager + Handler 组装，差别只在
//  1. 监听 127.0.0.1 随机端口（窗口专用，不暴露局域网）；
//  2. Start/Close 可编程生命周期（窗口开/关驱动），不做 signal 处理。
package webapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/pandaymx/lanchat/internal/discovery"
	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/internal/webui"
	"github.com/pandaymx/lanchat/pkg/appdir"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubserver"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/secure"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// discoverTimeout 是 -hub-url 留空时 mDNS 浏览局域网的等待上限（与 cmd/web 同值）。
const discoverTimeout = 5 * time.Second

// shutdownTimeout 是 Server.Close 里 http.Shutdown 的等待上限。
const shutdownTimeout = 5 * time.Second

// Options 是 Start 的入参，语义与 cmd/web runOptions 对齐。
type Options struct {
	// HubURL 是 hub 的 ws 地址；留空走 mDNS 自动发现，发现失败返回错误。
	HubURL string
	// User 是显示名（昵称即用）。
	User string
	// ConvID 是会话 ID；空用 webui.DefaultConversationID。
	ConvID string
	// Version 由构建注入（ldflags），用于页面 footer。
	Version string
	// Translator 是 UI chrome 文案翻译器；nil 时模板兜底 key 字面值。
	Translator i18n.Translator
	// Transport 是到 hub 的传输实现；nil 用 pkg/transport/ws 默认。
	Transport core.Transport
	// OnMessage 是实时新消息回调（M10.2 桌面端通知用）；nil 忽略。
	// 回调在事件泵 goroutine 同步调用，必须快速返回。
	OnMessage func(*protocol.StoredMessage)
	// DialTimeout 是单次拨号（含握手）上限；<=0 用 webui 默认（5s）。
	// 测试里给短值避免慢超时；桌面端一般不用改。
	DialTimeout time.Duration
	// EmbeddedHub 为 true 且 HubURL 为空时，进程内起嵌入式 hub
	// （pkg/hubserver，127.0.0.1 随机端口 + mesh 组网 + mDNS），
	// 免去"先另起 hub 进程"的部署步骤（去中心化迭代第一步）。
	// HubURL 非空时忽略（显式连外部 hub 优先）。
	EmbeddedHub bool
	// Embedded 是嵌入式 hub 的覆盖配置（可选）：DataDir/DBPath/FilesDir/
	// MDNS/NodeID/MeshPeers 由此覆盖；Addr 固定 127.0.0.1:0、Mesh 固定
	// true（webapp 语义），传入值忽略这两项。测试用临时目录隔离数据。
	Embedded *hubserver.Config
}

// Server 是桌面端持有的本地 web 服务。Start 成功后可取 URL 交给窗口加载，
// 窗口关闭时 Close 释放端口与 hub 连接。
type Server struct {
	ln  net.Listener
	srv *http.Server
	mgr *webui.Manager
	url string
	// hub 非 nil = 本进程嵌入式 hub（EmbeddedHub 模式）；hubCancel 是
	// 它的生命周期 ctx 取消函数，Close 时按序释放。
	hub       *hubserver.Server
	hubCancel context.CancelFunc
}

// Start 起本地 webui 服务并返回 Server。HubURL 空且 mDNS 找不到 hub 时返回
// 错误（与 cmd/web 同语义：找不到 hub 的窗口没有意义）。
func Start(opts Options) (*Server, error) {
	logger := logging.New("desktop")
	logger.Info("starting desktop webui", "version", opts.Version, "user", opts.User)

	// embHub/embCancel 是嵌入式 hub 的持有者（EmbeddedHub 模式才非零）。
	var embHub *hubserver.Server
	var embCancel context.CancelFunc
	if opts.HubURL == "" && opts.EmbeddedHub {
		// 去中心化迭代：本进程起嵌入式 hub（127.0.0.1 随机端口）。
		// 数据/身份与独立 hub 模式同目录（appdir 默认），mesh 组网
		// + mDNS 广播/发现——桌面端装上即用，无需先跑 hub 进程。
		// 统一走 hubserver.StartEmbedded（cmd/tui、cmd/web 同款）。
		embCtx, cancel := context.WithCancel(context.Background())
		var overrides hubserver.Config
		if opts.Embedded != nil {
			overrides = *opts.Embedded
		}
		overrides.Version = opts.Version
		hubSrv, wsURL, err := hubserver.StartEmbedded(embCtx, overrides)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("嵌入式 hub 启动失败: %w", err)
		}
		opts.HubURL = wsURL
		embHub, embCancel = hubSrv, cancel // 交给 Server 持有，Close 释放
		logger.Info("embedded hub started", "ws", opts.HubURL, "node", "auto")
	}
	if opts.HubURL == "" {
		discoverCtx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
		found, err := discovery.ResolveHubURL(discoverCtx, discoverTimeout)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("未指定 -hub-url 且自动发现失败: %w", err)
		}
		opts.HubURL = found
		logger.Info("mDNS discovered hub", "hub", found)
	}
	if opts.ConvID == "" {
		opts.ConvID = webui.DefaultConversationID
	}
	if opts.Transport == nil {
		// wire v2 传输加密：TOFU 信任 hub 公钥（首次连接记录、变化拒绝）。
		hubTrust, trustErr := secure.TrustForURL(opts.HubURL,
			filepath.Join(appdir.DataDir("lanchat"), "known_hubs.json"))
		if trustErr != nil {
			return nil, fmt.Errorf("信任存储初始化失败: %w", trustErr)
		}
		opts.Transport = wstransport.New().WithClientTrust(hubTrust)
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 0 // 交给 webui 默认
	}

	// Manager 按 cookie 惰性拨号；窗口内单用户单会话，沿用 web 端同一套。
	mgr := webui.NewManager(webui.ManagerConfig{
		HubURL:      opts.HubURL,
		User:        opts.User,
		ConvID:      opts.ConvID,
		Transport:   opts.Transport,
		Translator:  opts.Translator,
		DialTimeout: opts.DialTimeout,
		OnMessage:   opts.OnMessage,
	}, nil)

	mux := http.NewServeMux()
	h := webui.NewHandler(webui.Config{Version: opts.Version, Translator: opts.Translator}, mgr)
	h.Routes(mux)

	// 只监听回环：桌面窗口专用，不暴露到局域网。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		mgr.CloseAll()
		return nil, fmt.Errorf("listen loopback: %w", err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + ln.Addr().String()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("webui serve failed", "err", err)
		}
	}()

	logger.Info("desktop webui listening", "url", url, "hub", opts.HubURL)
	return &Server{ln: ln, srv: srv, mgr: mgr, url: url, hub: embHub, hubCancel: embCancel}, nil
}

// URL 返回窗口应加载的本地地址（http://127.0.0.1:<port>/）。
func (s *Server) URL() string { return s.url }

// Close 停掉本地服务并释放全部 hub 会话。窗口关闭时由调用方保证调用一次。
// 嵌入式 hub 模式还会把本进程的 hub 一并关掉（窗口=hub 生命周期）。
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	s.mgr.CloseAll()
	if s.hub != nil {
		if cerr := s.hub.Close(); cerr != nil && err == nil {
			err = cerr
		}
		s.hubCancel()
	}
	return err
}
