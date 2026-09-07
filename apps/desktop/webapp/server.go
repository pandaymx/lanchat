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
	"time"

	"github.com/pandaymx/lanchat/internal/discovery"
	"github.com/pandaymx/lanchat/internal/i18n"
	"github.com/pandaymx/lanchat/internal/webui"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
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
}

// Server 是桌面端持有的本地 web 服务。Start 成功后可取 URL 交给窗口加载，
// 窗口关闭时 Close 释放端口与 hub 连接。
type Server struct {
	ln  net.Listener
	srv *http.Server
	mgr *webui.Manager
	url string
}

// Start 起本地 webui 服务并返回 Server。HubURL 空且 mDNS 找不到 hub 时返回
// 错误（与 cmd/web 同语义：找不到 hub 的窗口没有意义）。
func Start(opts Options) (*Server, error) {
	logger := logging.New("desktop")
	logger.Info("starting desktop webui", "version", opts.Version, "user", opts.User)

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
		opts.Transport = wstransport.New()
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
	return &Server{ln: ln, srv: srv, mgr: mgr, url: url}, nil
}

// URL 返回窗口应加载的本地地址（http://127.0.0.1:<port>/）。
func (s *Server) URL() string { return s.url }

// Close 停掉本地服务并释放全部 hub 会话。窗口关闭时由调用方保证调用一次。
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	s.mgr.CloseAll()
	return err
}
