package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// ReceiveConfig 描述一次接收。
type ReceiveConfig struct {
	TransferID  string
	Token       string
	DestPath    string // 最终落盘路径
	Candidates  []string
	Size        int64
	SHA256      string
	DialTimeout time.Duration // 默认 3s
	ListenPort  int           // 反向监听端口（0 = 随机）
}

// Receive 执行：并发拨号（3s 超时，先胜者用）→ 恢复 .part/.meta（若有）→
// HELLO/token/resumeOffset → 收 DATA 写 .part → 回 ACK(cumOffset,credit) →
// FIN 校验 SHA-256 → rename。
//
// 清理语义：用户主动取消（ctx 取消或对端 CANCEL）删除 .part/.meta；
// 崩溃 / 断网 / 连接错误则保留，供下次续传。
func Receive(ctx context.Context, cfg ReceiveConfig, progress func(bytesDone, speedBps int64)) error {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 3 * time.Second
	}
	if len(cfg.Candidates) == 0 {
		return ErrDialFailed
	}

	// 多网卡治理：同 /24 优先排序，错峰并发拨号（Happy Eyeballs）。
	candidates := preferLocalSubnet(append([]string(nil), cfg.Candidates...))
	conn, err := dialFirst(ctx, candidates, cfg.DialTimeout, dialStagger)
	if err != nil {
		return err
	}
	defer conn.Close()
	return receiveOnConn(ctx, cfg, conn, progress)
}

// Receiver 在反向拨号场景下监听：发送方不可被拨入时，接收方监听 P2P 端口，
// 经 FILE_REVERSE 回传候选，由发送方拨入。连接建立后接收方主动发 HELLO。
type Receiver struct {
	cfg      ReceiveConfig
	listener net.Listener

	closeOnce sync.Once
}

// NewReceiver 创建反向监听的接收方。
func NewReceiver(cfg ReceiveConfig) (*Receiver, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ListenPort))
	if err != nil {
		return nil, fmt.Errorf("transfer: listen: %w", err)
	}
	return &Receiver{cfg: cfg, listener: ln}, nil
}

// Addr 返回实际监听地址。
func (r *Receiver) Addr() net.Addr { return r.listener.Addr() }

// CandidateAddrs 返回本机各活动网卡的 ip:port，用于组装 FILE_REVERSE.Candidates。
func (r *Receiver) CandidateAddrs() []string {
	_, port, err := net.SplitHostPort(r.listener.Addr().String())
	if err != nil {
		return nil
	}
	return candidateAddrs(port)
}

// Close 关闭监听端口。
func (r *Receiver) Close() {
	r.closeOnce.Do(func() { _ = r.listener.Close() })
}

// Serve 接受发送方的反向拨入并完成接收。
func (r *Receiver) Serve(ctx context.Context, progress func(bytesDone, speedBps int64)) error {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan acceptResult, 1)
	go func() {
		c, err := r.listener.Accept()
		resultCh <- acceptResult{conn: c, err: err}
	}()

	select {
	case a := <-resultCh:
		if a.err != nil {
			return fmt.Errorf("transfer: accept: %w", a.err)
		}
		defer a.conn.Close()
		return receiveOnConn(ctx, r.cfg, a.conn, progress)
	case <-ctx.Done():
		_ = r.listener.Close()
		<-resultCh
		return ctx.Err()
	}
}

// receiveOnConn 在已建立的连接上完成恢复、握手、接收与收尾。
func receiveOnConn(ctx context.Context, cfg ReceiveConfig, conn net.Conn, progress func(bytesDone, speedBps int64)) error {
	fr := protocol.NewFramer(conn)

	if err := os.MkdirAll(filepath.Dir(cfg.DestPath), 0o755); err != nil {
		return err
	}
	partPath := cfg.DestPath + ".part"
	metaPath := cfg.DestPath + ".meta"

	// 恢复：读取旧 meta 与 .part 实际大小，resumeOffset 取两者较小值。
	resume, err := recoverOffset(metaPath, partPath, &cfg)
	if err != nil {
		return err
	}

	// .part 不截断，续写；先对已有字节喂 hasher，保证全量校验。
	part, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	hasher := sha256.New()
	if resume > 0 {
		existing, err := os.Open(partPath)
		if err != nil {
			_ = part.Close()
			return err
		}
		if _, err := io.CopyN(hasher, existing, resume); err != nil {
			_ = existing.Close()
			_ = part.Close()
			return err
		}
		_ = existing.Close()
	}

	// 清理语义：
	//   - 用户主动取消：删除；
	//   - 握手阶段失败（坏 token / size 不符等，尚未开始收数据）：删除空残留；
	//   - 数据传输中断（崩溃 / 断网）：保留 .part/.meta 以便续传。
	userCanceled := false
	dataStarted := false
	defer func() {
		_ = part.Close()
		if userCanceled || (!dataStarted && resume == 0) {
			_ = os.Remove(partPath)
			_ = os.Remove(metaPath)
		}
	}()

	meta := &metaState{
		TransferID: cfg.TransferID,
		Name:       filepath.Base(cfg.DestPath),
		Size:       cfg.Size,
		SHA256:     cfg.SHA256,
		ChunkSize:  ChunkSize,
		BytesDone:  resume,
	}
	mw := newMetaWriter(metaPath, meta, MinCreditBytes, metaFlushInterval)
	if err := mw.flush(); err != nil {
		return err
	}

	// 握手：HELLO(真实 resumeOffset) → ACCEPT。
	if err := writeHello(fr, cfg.Token, resume); err != nil {
		return err
	}
	accept, err := readAccept(fr)
	if err != nil {
		return err
	}
	if accept.Size != cfg.Size {
		_ = writePeerError(fr, "size mismatch")
		return fmt.Errorf("%w: size %d want %d", errBadPayload, accept.Size, cfg.Size)
	}

	received := resume
	lastAck := resume
	start := time.Now()
	lastReport := start
	lastReportBytes := received
	dataStarted = true

	// 持续读帧直到 FIN。
	for {
		if err := ctx.Err(); err != nil {
			_ = writeCancel(fr)
			userCanceled = true
			return ErrCanceled
		}

		offset, data, err := readData(fr)
		if err != nil {
			if errors.Is(err, ErrCanceled) {
				// 对端主动 CANCEL：按用户取消清理。
				userCanceled = true
				return ErrCanceled
			}
			_ = mw.flush()
			return err
		}

		// offset 必须连续。
		if offset != received {
			_ = writePeerError(fr, fmt.Sprintf("non-contiguous offset %d want %d", offset, received))
			return fmt.Errorf("%w: offset %d want %d", errBadPayload, offset, received)
		}

		if _, err := part.WriteAt(data, received); err != nil {
			return err
		}
		_, _ = hasher.Write(data)
		received += int64(len(data))
		if err := mw.update(received, false); err != nil {
			return err
		}

		// 累计达到 1 MiB 就回 ACK，声明目标窗口 MaxCreditBytes。
		if received-lastAck >= MinCreditBytes || received == cfg.Size {
			if err := writeAck(fr, received, MaxCreditBytes); err != nil {
				return err
			}
			lastAck = received
		}

		// 进度回调。
		if progress != nil {
			now := time.Now()
			if now.Sub(lastReport) >= progressInterval || received == cfg.Size {
				elapsed := now.Sub(lastReport).Seconds()
				var speed int64
				if elapsed > 0 {
					speed = int64(float64(received-lastReportBytes) / elapsed)
				}
				progress(received, speed)
				lastReport = now
				lastReportBytes = received
			}
		}

		// 已收满，下一帧应为 FIN。
		if received == cfg.Size {
			break
		}
		if received > cfg.Size {
			_ = writePeerError(fr, "received more than declared size")
			return fmt.Errorf("%w: %d > %d", errBadPayload, received, cfg.Size)
		}
	}

	fin, err := readFin(fr)
	if err != nil {
		_ = mw.flush()
		return err
	}
	if fin.TotalBytes != received {
		_ = writePeerError(fr, "fin total bytes mismatch")
		return fmt.Errorf("%w: total %d want %d", errBadPayload, fin.TotalBytes, received)
	}

	// 全文件 SHA-256 校验。
	gotSum := hex.EncodeToString(hasher.Sum(nil))
	if gotSum != cfg.SHA256 {
		_ = writePeerError(fr, "checksum mismatch")
		_ = mw.flush()
		return ErrChecksum
	}

	if err := part.Sync(); err != nil {
		return err
	}
	if err := part.Close(); err != nil {
		return err
	}
	if err := os.Rename(partPath, cfg.DestPath); err != nil {
		return err
	}
	_ = os.Remove(metaPath)
	userCanceled = false
	return nil
}

// recoverOffset 计算续传起点：meta 记录值与 .part 实际大小的较小值。
// meta 与本次传输身份（TransferID/size/sha256）不一致时视为不可复用，从 0 开始。
func recoverOffset(metaPath, partPath string, cfg *ReceiveConfig) (int64, error) {
	partSize, err := fileSize(partPath)
	if err != nil {
		return 0, err
	}

	m, err := loadMeta(metaPath)
	if err != nil {
		return 0, err
	}
	if m == nil {
		// 无 meta：不可信的 .part 不能续写，截断重来由打开标志之外保证，
		// 这里直接返回 0 并移除可能残留的 .part。
		if partSize > 0 {
			if err := os.Remove(partPath); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}

	if m.TransferID != cfg.TransferID || m.Size != cfg.Size || m.SHA256 != cfg.SHA256 {
		// 属于另一次传输：清掉旧文件从头开始。
		if err := os.Remove(partPath); err != nil {
			return 0, err
		}
		if err := os.Remove(metaPath); err != nil {
			return 0, err
		}
		return 0, nil
	}

	offset := m.BytesDone
	if partSize < offset {
		offset = partSize
	}
	if offset < 0 {
		offset = 0
	}
	if offset > cfg.Size {
		offset = cfg.Size
	}
	return offset, nil
}

// fileSize 返回普通文件大小；不存在视为 0。
func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// metaFlushInterval 是 meta 定时落盘节流间隔。
const metaFlushInterval = 500 * time.Millisecond

// dialStagger 是多候选错峰拨号间隔（Happy Eyeballs）。
const dialStagger = 200 * time.Millisecond

// dialFirst 并发拨号候选地址，第一个成功者用，关闭其余连接。
// 按 index*stagger 错峰发起（Happy Eyeballs）：靠前候选优先，同时不浪费总预算。
// 全部失败返回 ErrDialFailed。
func dialFirst(ctx context.Context, candidates []string, timeout, stagger time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan result, len(candidates))
	var wg sync.WaitGroup
	for i, addr := range candidates {
		wg.Add(1)
		go func(index int, address string) {
			defer wg.Done()
			if stagger > 0 && index > 0 {
				select {
				case <-time.After(time.Duration(index) * stagger):
				case <-dialCtx.Done():
					ch <- result{err: dialCtx.Err()}
					return
				}
			}
			d := net.Dialer{}
			c, err := d.DialContext(dialCtx, "tcp", address)
			ch <- result{conn: c, err: err}
		}(i, addr)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	var pending []net.Conn
	for r := range ch {
		if r.err == nil {
			// 胜出：关闭其余已收集的在途连接。
			for _, c := range pending {
				_ = c.Close()
			}
			// 后台排空 channel，关闭后续成功的连接。
			go func() {
				for later := range ch {
					if later.conn != nil {
						_ = later.conn.Close()
					}
				}
			}()
			return r.conn, nil
		}
		if r.conn != nil {
			pending = append(pending, r.conn)
		}
	}
	return nil, ErrDialFailed
}
