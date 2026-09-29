package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"

	"github.com/pandaymx/lanchat/internal/appapi"
)

// maxFrameBytes 限制单个 JSON-RPC 帧大小：控制消息 ≤ 1 MiB，取与协议一致的上限。
const maxFrameBytes = 2 * 1024 * 1024

// Server 是 daemon 侧 IPC 服务端：监听本地传输（Unix socket / 命名管道），
// 以 JSON-RPC 2.0 暴露 appapi.API，并把 appapi.Listener 事件作为
// notification 广播给所有已连接的 UI。
type Server struct {
	api appapi.API

	mu     sync.Mutex
	conns  map[*conn]struct{}
	closed bool

	wg sync.WaitGroup
}

// NewServer 创建绑定到给定 appapi.API 的 IPC 服务端。
func NewServer(api appapi.API) *Server {
	return &Server{
		api:   api,
		conns: map[*conn]struct{}{},
	}
}

// Listener 返回应注册到 core 的事件适配器：core 事件经它广播给所有 UI。
func (s *Server) Listener() appapi.Listener { return eventBridge{srv: s} }

// serve 在已就绪的 listener 上接受连接并处理直到 listener 关闭。
func (s *Server) serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				s.wg.Wait()
				return nil
			}
			return err
		}
		s.addConn(c)
	}
}

// Close 停止接受新连接并断开全部已连接 UI。
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		c.close()
	}
}

func (s *Server) addConn(nc net.Conn) {
	c := &conn{srv: s, conn: nc}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()

	s.wg.Add(1)
	go s.handle(c)
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// broadcast 把 notification 发送给所有已连接 UI。
func (s *Server) broadcast(method string, params interface{}) {
	data, err := encode(notification{JSONRPC: rpcVersion, Method: method, Params: params})
	if err != nil {
		return
	}
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		c.write(data)
	}
}

// conn 表示一条到 UI 的 IPC 连接。
type conn struct {
	srv  *Server
	conn net.Conn

	writeMu sync.Mutex
}

func (c *conn) write(data []byte) {
	c.writeMu.Lock()
	_, err := c.conn.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		c.close()
	}
}

func (c *conn) close() { _ = c.conn.Close() }

// handle 逐行读取 JSON-RPC 请求并回包，连接结束时清理。
func (s *Server) handle(c *conn) {
	defer s.wg.Done()
	defer s.removeConn(c)
	defer c.close()

	reader := bufio.NewReaderSize(c.conn, maxFrameBytes)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.dispatch(c, line)
		}
		if err != nil {
			// EOF / 连接关闭 / 其他读错误均直接结束该连接，无需回包。
			return
		}
	}
}

// dispatch 解析并处理单条请求；notification 不回包。
func (s *Server) dispatch(c *conn, line []byte) {
	id := json.RawMessage(nil)
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		c.write(mustEncode(errResponse(id, codeParseError, "解析失败：消息不是合法 JSON")))
		return
	}
	if req.JSONRPC != rpcVersion || req.Method == "" {
		c.write(mustEncode(errResponse(req.ID, codeInvalidReq, "非法请求：缺少 jsonrpc 或 method")))
		return
	}
	if req.isNotification() {
		// daemon ← UI 方向契约没有 notification，直接忽略。
		return
	}

	h, ok := handlers[req.Method]
	if !ok {
		c.write(mustEncode(errResponse(req.ID, codeNoSuchMethod, "未知方法: "+req.Method)))
		return
	}
	result, rerr := h(s.api, req.Params)
	if rerr != nil {
		c.write(mustEncode(errResponse(req.ID, rerr.code, rerr.message)))
		return
	}
	c.write(mustEncode(resultResponse(req.ID, result)))
}

func mustEncode(v Response) []byte {
	data, err := encode(v)
	if err != nil {
		data, _ = encode(errResponse(v.ID, codeInternal, "内部序列化失败"))
	}
	return data
}
