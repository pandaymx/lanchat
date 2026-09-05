package ws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// DefaultPath 是 Listen 时接受 WebSocket upgrade 的 HTTP 路径。
//
// 限定路径有两个作用：
//  1. 浏览器直接访问 http://host:9000/ 时不会误建立 WS 连接（M4 那里要放 Web UI）
//  2. 健康检查 / 静态资源可以走别的路径，互不干扰
const DefaultPath = "/ws"

// Transport 是 core.Transport 的 WebSocket 实现。
//
// 同一个实例既可作客户端（Dial）也可作服务端（Listen）——
// 两种角色互不干扰，字段上也没有共享状态。
type Transport struct {
	// path 是 Listen 接受 upgrade 的路径，为空时取 DefaultPath。
	path string
}

// New 创建一个 WebSocket Transport。零值也可用（走默认路径）。
func New() *Transport { return &Transport{} }

// WithPath 设置 Listen 接受 upgrade 的 HTTP 路径，返回新的 Transport。
// 这是一个可选的构造期配置，不影响已建立的连接。
func (t *Transport) WithPath(path string) *Transport {
	cp := *t
	cp.path = path
	return &cp
}

// pathOrDefault 取生效的 upgrade 路径。
func (t *Transport) pathOrDefault() string {
	if t.path != "" {
		return t.path
	}
	return DefaultPath
}

// ---- 客户端角色 ----

// Dial 拨号到 target 并建立 WebSocket 连接。
//
// target 支持两种写法：
//   - "ws://127.0.0.1:9000"   完整 URL
//   - "127.0.0.1:9000"        裸地址（自动补 ws://，路径用 DefaultPath）
//
// hello 里的 DeviceID 会被记在返回的 Conn 上（供 LocalDeviceID 查询）。
// 注意：握手帧 FKHello 不由 Dial 发送——那是 pkg/client.Connect 的职责。
// 这样拨号与握手解耦，重连时可以复用同一条连接逻辑。
func (t *Transport) Dial(ctx context.Context, target string, hello protocol.Hello) (core.Conn, error) {
	u, err := normalizeTarget(target, t.pathOrDefault())
	if err != nil {
		return nil, err
	}

	// 第二个返回值是 HTTP 响应，成功时其 Body 已被库置为 nil
	// （库文档：You never need to close resp.Body yourself）。
	// 失败时连接为 nil，也没有可关闭的资源。故此处忽略它是正确的。
	//nolint:bodyclose // coder/websocket 接管了底层连接，Body 不由调用方关闭
	wsConn, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", u, err)
	}
	return newConn(wsConn, hello.DeviceID), nil
}

// normalizeTarget 把 target 补成完整的 ws:// URL。
func normalizeTarget(target, path string) (string, error) {
	if target == "" {
		return "", errors.New("ws: empty target")
	}
	if !strings.Contains(target, "://") {
		target = "ws://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse target %q: %w", target, err)
	}
	// 只保留 scheme://host，路径统一用配置的那个：
	// 调用方传 "ws://host:9000/foo" 这种多半是手误，不该静默接受
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// ---- 服务端角色 ----

// readHeaderTimeout 是 HTTP 请求头读取超时，防慢速攻击（Slowloris）。
const readHeaderTimeout = 10 * time.Second

// shutdownTimeout 是 ctx 取消后等待在途请求收尾的时间。
const shutdownTimeout = 5 * time.Second

// Listen 在 addr 起 HTTP 服务，把 upgrade 成功的连接通过 onConn 交给调用方。
//
// 运行直到 ctx 取消，之后优雅关停并返回 ctx.Err()。
//
// 关于 onConn 的 hello 参数：WebSocket 的握手是 HTTP 层的，拿不到应用层身份，
// 所以这里传的是**零值 Hello**（表示"尚未握手"）。真正的身份由客户端
// 连上后发的第一帧 FKHello 声明，由 hubstate.Router 处理并登记。
// 这是 WebSocket 与 fake transport 的一个差异——后者能在 accept 时就知道身份。
//
// 并发模型：每条连接起一个 goroutine 调 onConn，
// 这样某个回调卡住不会阻塞 accept 循环（新客户端照样能连进来）。
func (t *Transport) Listen(ctx context.Context, addr string, onConn func(core.Conn, protocol.Hello) error) error {
	path := t.pathOrDefault()

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		wsConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			// upgrade 失败：可能是普通 HTTP 请求或非 WS 客户端。
			// 不写响应体——Accept 失败时已经写过了，再写会报 superfluous WriteHeader。
			return
		}
		c := newConn(wsConn, "")
		go func() {
			if err := onConn(c, protocol.Hello{}); err != nil {
				_ = c.Close()
			}
		}()
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	// ctx 取消 → 关停 HTTP 服务 → Serve 返回 → Listen 返回 ctx.Err()
	//
	// 用 WithoutCancel 而非 Background：ctx 此时已取消，
	// 直接传它 Shutdown 会立刻放弃等待；而我们希望给在途的 HTTP 握手
	// 一点收尾时间。WithoutCancel 保留 ctx 的值（如 trace id）但脱离取消链。
	go func() {
		<-ctx.Done()
		// WithoutCancel 脱离已取消的父链，再套一层超时做有界等待：
		// 既不因父 ctx 已取消而立即放弃，也不会无限期卡住。
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		// 监听失败（端口占用等）是真实错误，要往上抛
		return fmt.Errorf("serve: %w", err)
	}
	return ctx.Err()
}
