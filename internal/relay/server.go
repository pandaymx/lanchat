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

// pairOffer 是后到者发给先到者的配对请求：携带后到连接与其握手用的
// bufio.Reader（可能已缓冲握手行之后的早期字节），并通过 ack 把先到连接
// 回交给后到者。done 由桥接方在转发结束后关闭，通知后到者退出。
type pairOffer struct {
	conn net.Conn
	br   io.Reader
	ack  chan net.Conn
	done chan struct{}
}

// pairResult 是配对结果。bridge=true 表示本端是先到者，负责双向桥接；
// peerReader 是对端的握手 bufio，仅桥接方需要使用；桥接方结束后必须
// close(done) 以通知后到者退出。
type pairResult struct {
	peer       net.Conn
	peerReader io.Reader
	done       chan struct{}
	bridge     bool
}

// Server 是中继 TCP 服务器。
type Server struct {
	listener net.Listener

	mu      sync.Mutex
	waiting map[string]chan *pairOffer // relayID -> 先到者的等待 channel
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
		waiting:  make(map[string]chan *pairOffer),
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

	res, err := s.pair(relayID, conn, br)
	if err != nil {
		return // 等待超时或服务器关闭
	}
	defer res.peer.Close()

	// 只有先到者一方做双向桥接：两个 handle 若各自 io.Copy 同一对连接，
	// 每个方向都会被两个 reader 竞争读取（其中一个还是 bufio 预读），小帧
	// 时序下会把字节流劈碎、重复，造成对端解密失败 / broken pipe。
	if !res.bridge {
		// 后到者：桥接由对端负责，本端只等转发结束再走 defer 关闭。
		<-res.done
		return
	}

	// 双向透传。读两侧都必须用各自的 bufio.Reader：握手行之后提前到达的
	// 字节可能已被缓冲，改读裸 conn 会丢掉这部分数据。
	copyDone := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(res.peer, br)
		_ = closeWrite(res.peer)
		copyDone <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, res.peerReader)
		_ = closeWrite(conn)
		copyDone <- struct{}{}
	}()
	// 必须等两个方向都结束：只等一个方向会在对端半关闭后提前关闭两条连接，
	// 拖慢另一方向尚在传输的尾数据 / ACK（race 构建下尤其易触发）。
	<-copyDone
	<-copyDone
	// 通知后到者桥接已结束（其 handle 正在等待）。
	close(res.done)
}

// closeWrite 在连接支持时半关闭写方向，让对端读到 EOF 而不是 RST。
func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// pair 按 relayID 配对。先到者注册一个等待 channel 并阻塞；后到者投递
// pairOffer（携带自身连接与握手 bufio），再从 offer.ack 取回先到连接。
// 两个方向各用独立 channel，任何调度时序下都不会把连接错配给自己。
func (s *Server) pair(relayID string, conn net.Conn, br io.Reader) (pairResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return pairResult{}, errors.New("relay closed")
	}
	waiter, exists := s.waiting[relayID]
	if !exists {
		// 本端是先到者：注册并等待后到者的 offer。
		q := make(chan *pairOffer, 1)
		s.waiting[relayID] = q
		s.mu.Unlock()

		timer := time.NewTimer(s.ttl())
		defer timer.Stop()
		select {
		case offer := <-q:
			// 把本端连接回交给后到者；缓冲为 1，即使对方尚未进入接收也不丢。
			offer.ack <- conn
			// 先到者负责桥接：对端读取使用后到者移交的 bufio。
			return pairResult{
				peer:       offer.conn,
				peerReader: offer.br,
				done:       offer.done,
				bridge:     true,
			}, nil
		case <-timer.C:
			s.removeWaiting(relayID, q)
			return pairResult{}, errors.New("relay: pairing timeout")
		}
	}

	// 本端是后到者：摘下等待项并投递 offer。
	delete(s.waiting, relayID)
	s.mu.Unlock()

	offer := &pairOffer{
		conn: conn,
		br:   br,
		ack:  make(chan net.Conn, 1),
		done: make(chan struct{}),
	}
	timer := time.NewTimer(s.ttl())
	defer timer.Stop()
	select {
	case waiter <- offer:
	case <-timer.C:
		// 先到者恰好在我们到达前后超时退出，offer 无人接收。
		return pairResult{}, errors.New("relay: pairing timeout")
	}
	select {
	case peer := <-offer.ack:
		// 后到者不做桥接，等待先到者转发结束。
		return pairResult{peer: peer, done: offer.done, bridge: false}, nil
	case <-timer.C:
		return pairResult{}, errors.New("relay: pairing timeout")
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
func (s *Server) removeWaiting(relayID string, q chan *pairOffer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.waiting[relayID]; ok && cur == q {
		delete(s.waiting, relayID)
	}
}
