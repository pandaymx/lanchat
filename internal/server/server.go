// Package server 实现 LANChat 中心信令服务器：
// WebSocket 接入、PSK 鉴权、revision 在线表、心跳保活与文本/表情单播中转。
//
// 控制面只转发不超过 1 MiB 的消息，消息不落盘、不补历史。
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// Options 是服务器运行参数（由 cmd 从 config.Config 映射）。
type Options struct {
	Listen            string
	Path              string
	AuthMode          string
	PSKHash           string
	HeartbeatInterval time.Duration
	IdleTimeout       time.Duration
	ShutdownGrace     time.Duration
}

// Server 是信令服务器。
type Server struct {
	opts  Options
	hub   *hub
	mux   *http.ServeMux
	http  *http.Server
	ln    net.Listener
	ready chan struct{} // 监听成功后关闭
	logf  func(string, ...any)
}

// New 校验参数并构造服务器（尚未开始监听）。
func New(opts Options, logf func(string, ...any)) (*Server, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		opts:  opts,
		hub:   newHub(opts, logf),
		mux:   http.NewServeMux(),
		ready: make(chan struct{}),
		logf:  logf,
	}
	s.mux.HandleFunc(opts.Path, s.handleWS)
	s.http = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

func validateOptions(o Options) error {
	if o.Listen == "" {
		return errors.New("Listen 不能为空")
	}
	if o.Path == "" || !strings.HasPrefix(o.Path, "/") {
		return fmt.Errorf("Path 必须以 / 开头，当前 %q", o.Path)
	}
	if o.AuthMode != "psk" && o.AuthMode != "none" {
		return fmt.Errorf("非法 AuthMode %q", o.AuthMode)
	}
	if o.AuthMode == "psk" && o.PSKHash == "" {
		return errors.New("AuthMode=psk 时必须提供 PSKHash")
	}
	if o.HeartbeatInterval <= 0 || o.IdleTimeout <= 0 || o.ShutdownGrace <= 0 {
		return errors.New("HeartbeatInterval/IdleTimeout/ShutdownGrace 必须为正")
	}
	return nil
}

// Ready 在服务器成功开始监听后关闭，可用于等待就绪。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// ListenAddr 返回实际监听地址（端口 0 时为系统分配的真实端口）。
// 调用前应先等待 Ready()。
func (s *Server) ListenAddr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Serve 开始监听并服务，直到 ctx 取消（触发优雅关停）或发生不可恢复错误。
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", s.opts.Listen, err)
	}
	s.ln = ln
	close(s.ready)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go s.hub.run(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.http.Serve(ln) }()

	select {
	case <-ctx.Done():
		graceCtx, gcancel := context.WithTimeout(context.Background(), s.opts.ShutdownGrace)
		defer gcancel()
		err := s.http.Shutdown(graceCtx)
		select {
		case <-serveErr:
		case <-graceCtx.Done():
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-serveErr:
		cancel()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// handleWS 接受 WebSocket 连接并驱动该连接的读泵。
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// 局域网可信场景，原生客户端不发 Origin，关闭 Origin 校验。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(protocol.MaxControlPayload))

	host := r.RemoteAddr
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	c := newClient(conn, host)

	go c.writePump()

	if err := s.handshake(r.Context(), c); err != nil {
		s.logf("握手失败（%s）: %v", c.addr, err)
		c.terminate()
		return
	}

	s.readLoop(r.Context(), c)
	s.hub.submit(hubEvent{kind: evUnregister, client: c})
	c.terminate()
}

// handshake 处理首帧 HELLO：鉴权 → WELCOME → USER_LIST → register。
func (s *Server) handshake(parent context.Context, c *Client) error {
	env, err := readEnvelope(parent, c, helloWait)
	if err != nil {
		return err
	}
	if env.Type != protocol.Hello {
		s.mustEnqueue(c, protocol.Error, protocol.ErrorPayload{
			Code: "expected_hello", Message: "首帧必须是 HELLO",
		})
		return errors.New("首帧不是 HELLO")
	}

	var hello protocol.HelloPayload
	if err := env.DecodePayload(&hello); err != nil {
		return fmt.Errorf("解码 HELLO: %w", err)
	}

	if reason, ok := s.hub.auth.helloOK(c.addr, hello.PSK); !ok {
		s.mustEnqueue(c, protocol.AuthFail, protocol.AuthFailPayload{
			Reason: reason, Retryable: true,
		})
		return errors.New("鉴权失败: " + reason)
	}

	c.id = hello.DeviceID
	if c.id == "" {
		c.id = uuid.NewString()
	}
	c.nickname = hello.Nickname
	c.os = hello.OS

	// 先同步完成注册，拿到含自己的全量在线表，再下发 WELCOME/USER_LIST。
	rev, users := s.hub.registerSnapshot(parent, c)
	s.mustEnqueue(c, protocol.Welcome, protocol.WelcomePayload{
		SelfID:            c.id,
		ProtocolVersion:   protocol.ProtocolVersion,
		HeartbeatInterval: int(s.opts.HeartbeatInterval / time.Second),
		AuthMode:          s.opts.AuthMode,
		Features:          negotiateFeatures(hello.Caps),
	})
	s.mustEnqueue(c, protocol.UserList, protocol.UserListPayload{
		Revision: rev, Users: users,
	})
	return nil
}

// readLoop 持续读取注册后的上行帧并转交给 hub。
func (s *Server) readLoop(parent context.Context, c *Client) {
	for {
		env, err := readEnvelope(parent, c, 0)
		if err != nil {
			return
		}
		switch env.Type {
		case protocol.Heartbeat, protocol.UserListReq, protocol.TextMsg,
			protocol.StickerMsg, protocol.Typing:
			s.hub.submit(hubEvent{kind: evRoute, client: c, env: env})
		case protocol.Hello:
			s.mustEnqueue(c, protocol.Error, protocol.ErrorPayload{
				Code: "unexpected_hello", Message: "HELLO 只能在握手阶段发送",
			})
		default:
			s.hub.submit(hubEvent{kind: evRoute, client: c, env: env})
		}
	}
}

// readEnvelope 读取一条 WS 文本帧并解码信封；wait>0 时作为读超时上限。
func readEnvelope(parent context.Context, c *Client, wait time.Duration) (*protocol.Envelope, error) {
	ctx := parent
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, wait)
		defer cancel()
	}
	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("只接受文本帧")
	}
	c.touch() // 任意入站帧均视为活动，刷新空闲计时（含握手 HELLO）
	return protocol.UnmarshalEnvelope(data)
}

// dropClient 踢除慢客户端：终止连接并通知 hub 下线（未注册时 hub 会忽略）。
func (s *Server) dropClient(c *Client) {
	c.terminate()
	s.hub.submit(hubEvent{kind: evUnregister, client: c})
}

func newMsgID() string { return uuid.NewString() }
