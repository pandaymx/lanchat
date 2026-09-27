package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// progressInterval 进度回调的固定间隔。
const progressInterval = 200 * time.Millisecond

// SendConfig 描述一次发送。
type SendConfig struct {
	TransferID string
	File       *os.File
	Name       string
	Size       int64
	SHA256     string
	Token      string
	ListenPort int // P2P 监听端口（0 = 随机）
}

// Sender 在 P2P 监听端口上接受一个接收方连接并完成发送。
type Sender struct {
	cfg      SendConfig
	listener net.Listener

	closeOnce sync.Once
}

// NewSender 创建监听 P2P 端口的发送方。
func NewSender(cfg SendConfig) (*Sender, error) {
	if cfg.File == nil || cfg.Token == "" {
		return nil, errors.New("transfer: file and token are required")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ListenPort))
	if err != nil {
		return nil, fmt.Errorf("transfer: listen: %w", err)
	}
	return &Sender{cfg: cfg, listener: ln}, nil
}

// Addr 返回实际 P2P 监听地址。
func (s *Sender) Addr() net.Addr {
	return s.listener.Addr()
}

// CandidateAddrs 返回本机各活动网卡的 ip:port，用于组装 FILE_OFFER.Candidates。
func (s *Sender) CandidateAddrs() []string {
	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return nil
	}
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipnet.IP.To4()
			if v4 == nil {
				continue
			}
			out = append(out, net.JoinHostPort(v4.String(), port))
		}
	}
	return out
}

// Close 关闭监听端口。
func (s *Sender) Close() {
	s.closeOnce.Do(func() { _ = s.listener.Close() })
}

// Serve 接受并服务一个接收方直到 FIN 完成或 ctx 取消。
func (s *Sender) Serve(ctx context.Context, progress func(bytesDone, speedBps int64)) error {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan acceptResult, 1)
	go func() {
		c, err := s.listener.Accept()
		resultCh <- acceptResult{conn: c, err: err}
	}()

	select {
	case r := <-resultCh:
		if r.err != nil {
			return fmt.Errorf("transfer: accept: %w", r.err)
		}
		defer r.conn.Close()
		return s.serveConn(ctx, r.conn, progress)
	case <-ctx.Done():
		// 关闭 listener 以解除 Accept 阻塞。
		_ = s.listener.Close()
		<-resultCh
		return ctx.Err()
	}
}

// serveConn 在一条已建立连接上完成握手、数据发送与收尾。
func (s *Sender) serveConn(ctx context.Context, conn net.Conn, progress func(bytesDone, speedBps int64)) error {
	fr := protocol.NewFramer(conn)

	// 1. 读 HELLO，校验一次性 token（M2 单任务，TransferID 经信令已绑定到本 Sender）。
	hello, err := readHello(fr)
	if err != nil {
		return err
	}
	if hello.Token != s.cfg.Token {
		_ = writePeerError(fr, "bad token")
		return ErrBadToken
	}

	// 2. 回 ACCEPT，初始窗口 1 MiB。
	if err := writeAccept(fr, s.cfg.Size, ChunkSize); err != nil {
		return err
	}

	cw := newCreditWindow(MinCreditBytes)
	buf := make([]byte, ChunkSize)

	// ackErr 承载 ACK 读取 goroutine 的结果。
	ackCh := make(chan ackResult, 8)
	ackCtx, stopAck := context.WithCancel(ctx)
	defer stopAck()
	go s.readAcks(ackCtx, fr, ackCh)

	var bytesSent int64
	start := time.Now()
	lastReport := start
	lastReportBytes := int64(0)

	report := func(now time.Time, force bool) {
		if progress == nil {
			return
		}
		if !force && now.Sub(lastReport) < progressInterval {
			return
		}
		elapsed := now.Sub(lastReport).Seconds()
		var speed int64
		if elapsed > 0 {
			speed = int64(float64(bytesSent-lastReportBytes) / elapsed)
		}
		progress(bytesSent, speed)
		lastReport = now
		lastReportBytes = bytesSent
	}

	// 3. 信用窗口循环发送。
	for bytesSent < s.cfg.Size {
		// ctx 取消优先。
		if err := ctx.Err(); err != nil {
			_ = writeCancel(fr)
			return ErrCanceled
		}

		n, err := s.cfg.File.ReadAt(buf, bytesSent)
		if err != nil && n == 0 {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		// 在窗口内取本次可发量；窗口耗尽则等 ACK。
		for sent := 0; sent < n; {
			take := cw.acquire(n - sent)
			if take == 0 {
				select {
				case r := <-ackCh:
					if r.err != nil {
						if errors.Is(r.err, ErrCanceled) {
							return ErrCanceled
						}
						return r.err
					}
					cw.grant(r.cumOffset, int64(r.credit))
				case <-ctx.Done():
					_ = writeCancel(fr)
					return ErrCanceled
				}
				continue
			}
			if err := writeData(fr, bytesSent+int64(sent), buf[sent:sent+take]); err != nil {
				return err
			}
			sent += take
			bytesSent += int64(take)
			report(time.Now(), false)
		}
	}

	// 排空可能残留的 ACK（非阻塞），让累计确认点尽量追上，但不强制等待。
	for {
		select {
		case r := <-ackCh:
			if r.err == nil {
				cw.grant(r.cumOffset, int64(r.credit))
			}
		default:
			goto fin
		}
	}
fin:
	// 4. 全部发完，发 FIN。
	if err := writeFin(fr, s.cfg.SHA256, bytesSent); err != nil {
		return err
	}
	report(time.Now(), true)

	// 5. 优雅断连：半关闭发送方向（FIN 字节排在所有数据之后），
	// 等待接收方读完 FIN 帧并关闭其端；readAcks 读到 EOF 后回送错误结果。
	// 不立即全关闭，否则接收方仍在写末尾 ACK 时会收到 RST（broken pipe）。
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	grace := time.NewTimer(5 * time.Second)
	defer grace.Stop()
	for {
		select {
		case r := <-ackCh:
			if r.err != nil {
				return nil // 对端已关闭
			}
			cw.grant(r.cumOffset, int64(r.credit))
		case <-grace.C:
			return nil // 兜底超时，交由 defer 全关闭
		case <-ctx.Done():
			return nil
		}
	}
}

// ackResult 是一条 ACK 的读取结果。
type ackResult struct {
	cumOffset int64
	credit    uint32
	err       error
}

// readAcks 持续读取 ACK 帧并送入 ch，直到出错或 ctx 取消。
func (s *Sender) readAcks(ctx context.Context, fr *protocol.Framer, ch chan<- ackResult) {
	for {
		cum, credit, err := readAck(fr)
		if err != nil {
			select {
			case ch <- ackResult{err: err}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case ch <- ackResult{cumOffset: cum, credit: credit}:
		case <-ctx.Done():
			return
		}
	}
}
