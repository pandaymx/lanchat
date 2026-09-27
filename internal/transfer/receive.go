package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
}

// Receive 执行：并发拨号（3s 超时，先胜者用）→ HELLO/token →
// 收 DATA 写 .part → 回 ACK(cumOffset,credit) → FIN 校验 SHA-256 → rename。
func Receive(ctx context.Context, cfg ReceiveConfig, progress func(bytesDone, speedBps int64)) error {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 3 * time.Second
	}
	if len(cfg.Candidates) == 0 {
		return ErrDialFailed
	}

	conn, err := dialFirst(ctx, cfg.Candidates, cfg.DialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	fr := protocol.NewFramer(conn)

	// 握手：HELLO → ACCEPT。
	if err := writeHello(fr, cfg.Token, 0); err != nil {
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

	// 打开 .part 临时文件。
	if err := os.MkdirAll(filepath.Dir(cfg.DestPath), 0o755); err != nil {
		return err
	}
	partPath := cfg.DestPath + ".part"
	part, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	// 收数据失败/取消时清理 .part；校验成功后 rename 会移走它。
	cleanup := true
	defer func() {
		_ = part.Close()
		if cleanup {
			_ = os.Remove(partPath)
		}
	}()

	hasher := sha256.New()
	var received int64
	lastAck := int64(0)
	start := time.Now()
	lastReport := start
	lastReportBytes := int64(0)

	// 持续读帧直到 FIN。
	for {
		if err := ctx.Err(); err != nil {
			_ = writeCancel(fr)
			return ErrCanceled
		}

		offset, data, err := readData(fr)
		if err != nil {
			if errors.Is(err, ErrCanceled) {
				return ErrCanceled
			}
			return err
		}

		// offset 必须连续。
		if offset != received {
			_ = writePeerError(fr, fmt.Sprintf("non-contiguous offset %d want %d", offset, received))
			return fmt.Errorf("%w: offset %d want %d", errBadPayload, offset, received)
		}

		if _, err := part.Write(data); err != nil {
			return err
		}
		_, _ = hasher.Write(data)
		received += int64(len(data))

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
	cleanup = false
	return nil
}

// dialFirst 并发拨号候选地址，第一个成功者用，关闭其余连接。
// 全部失败返回 ErrDialFailed。
func dialFirst(ctx context.Context, candidates []string, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan result, len(candidates))
	var wg sync.WaitGroup
	for _, addr := range candidates {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			d := net.Dialer{}
			c, err := d.DialContext(dialCtx, "tcp", address)
			ch <- result{conn: c, err: err}
		}(addr)
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
