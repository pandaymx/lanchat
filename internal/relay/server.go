// Package relay 实现回退中继数据面：直连失败的两个客户端各自连到中继，
// 中继按 relayID 把两条连接配对，双向透传字节，字节不落地、不解析。
//
// 安全：中继只转发上层（transfer）AES-GCM 加密后的密文，本身无密钥，
// 无法解密文件内容。
package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// 默认参数。
const (
	// handshakeTimeout 是配对握手（relayID 行）的读取上限。
	handshakeTimeout = 10 * time.Second
	// pairingTTL 是单边到达后等待另一端的最长时间。
	pairingTTL = 30 * time.Second
)

// Server 是中继 TCP 服务器。
type Server struct {
	listener net.Listener

	mu      sync.Mutex
	waiting map[string]chan net.Conn // relayID -> 先到连接的通知 channel
	closed  bool

	// pairTTL 为零值时使用默认 pairingTTL；测试可覆盖。
	pairTTL time.Duration

	closeOnce sync.Once
}

// New 在 listen（如 ":19100"）上启动中继服务器。
func New(listen string) (*Server, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("relay: listen: %w", err)
	}
	s := &Server{
		listener: ln,
		waiting:  make(map[string]chan net.Conn),
	}
	return s, nil
}

// Addr 返回实际监听地址。
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// AdvertiseAddr 返回对客户端宣告的中继数据面地址：用监听端口与给定 host
// 组合（监听 ":19100" 时 host 常取本机 LAN IP）。host 为空时使用监听地址中的 host。
func (s *Server) AdvertiseAddr(host string) string {
	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return s.listener.Addr().String()
	}
	if host == "" {
		host, _, _ = net.SplitHostPort(s.listener.Addr().String())
	}
	return net.JoinHostPort(host, port)
}

// Serve 接受连接直到 Close 或 ctx 取消。
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("relay: accept: %w", err)
		}
		go s.handle(conn)
	}
}

// Close 关闭监听。
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.listener.Close()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	})
	return err
}

// handle 读取一行 relayID 作为握手，然后与持有相同 relayID 的另一端配对。
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	relayID := line[:len(line)-1]
	if relayID == "" {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	peer, err := s.pair(relayID, conn)
	if err != nil {
		return // 等待超时或服务器关闭
	}
	defer peer.Close()

	// 双向透传：任一方关闭/出错即结束，交由两个 defer 关闭连接。
	// 从本端读出时必须用 br：握手行之后提前到达的字节可能已被 bufio 缓冲，
	// 改读裸 conn 会丢掉这部分数据。
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(peer, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, peer); done <- struct{}{} }()
	// 必须等两个方向都结束：只等一个方向会在对端半关闭后提前关闭两条连接，
	// 拖慢另一方向尚在传输的尾数据 / ACK（race 构建下尤其易触发）。
	<-done
	<-done
}

// pair 按 relayID 配对：先到者放入 waiting 并等待后到者；后到者取出先到连接。
func (s *Server) pair(relayID string, conn net.Conn) (net.Conn, error) {
	ch := make(chan net.Conn, 1)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("relay closed")
	}
	if waiter, ok := s.waiting[relayID]; ok {
		delete(s.waiting, relayID)
		s.mu.Unlock()
		// 本端是后到者：通知先到者配对成功，并把对端交给它。
		select {
		case waiter <- conn:
		default:
		}
		// 对后到者而言，对端是先到连接，需要从 waiter 取回。
		peer := <-waiter
		return peer, nil
	}
	s.waiting[relayID] = ch
	s.mu.Unlock()

	// 本端是先到者：等待后到者，受 pairTTL 约束。
	timer := time.NewTimer(s.ttl())
	defer timer.Stop()
	select {
	case peer := <-ch:
		// 后到者已把自己的连接送入 ch；但同时后到者也在等先到连接。
		// 回送本端连接以完成握手。
		select {
		case ch <- conn:
		default:
		}
		return peer, nil
	case <-timer.C:
		s.removeWaiting(relayID, ch)
		return nil, errors.New("relay: pairing timeout")
	}
}

// ttl 返回单边等待超时时长。
func (s *Server) ttl() time.Duration {
	if s.pairTTL > 0 {
		return s.pairTTL
	}
	return pairingTTL
}

// removeWaiting 仅在 channel 仍是当前注册项时移除，避免误删后来者。
func (s *Server) removeWaiting(relayID string, ch chan net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.waiting[relayID]; ok && cur == ch {
		delete(s.waiting, relayID)
	}
}
