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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// MultipathPathMetric 记录多路径并发传输中单条网卡/隧道路径的实测 RTT 与吞吐表现（v7.3.0）。
type MultipathPathMetric struct {
	Candidate     string        `json:"candidate"`
	LinkType      string        `json:"linkType"` // "lan" | "tailscale" | "overlay"
	RTT           time.Duration `json:"rtt"`
	ThroughputBps int64         `json:"throughputBps"`
	ChunksDone    int           `json:"chunksDone"`
	BytesDone     int64         `json:"bytesDone"`
	FailedCount   int           `json:"failedCount"`
}

// MultipathStats 汇总一次多路径并发传输的聚合统计（v7.3.0）。
type MultipathStats struct {
	PathsUsed     int                   `json:"pathsUsed"`
	TotalChunks   int                   `json:"totalChunks"`
	FailoverCount int                   `json:"failoverCount"`
	Routes        []MultipathPathMetric `json:"routes"`
}

type multipathChunk struct {
	index int
	start int64
	end   int64
	tries int
}

// classifyCandidateLink 识别候选地址属于物理局域网（lan）、Tailscale CGNAT（tailscale）或跨网段（overlay）。
func classifyCandidateLink(candidate string) string {
	ip := parseCandidateIP(candidate)
	if ip == nil {
		return "overlay"
	}
	if isCGNATIP(ip) {
		return "tailscale"
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return "lan"
	}
	return "overlay"
}

// ProbeAndRankCandidates 并发探测多网卡候选地址，按同子网优先级与实测 TCP 握手 RTT 排序返回可达路径（v7.3.0）。
func ProbeAndRankCandidates(ctx context.Context, candidates []string, timeout time.Duration) []MultipathPathMetric {
	if len(candidates) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 600 * time.Millisecond
	}
	ordered := preferLocalSubnet(append([]string(nil), candidates...))
	seen := make(map[string]bool, len(ordered))
	unique := make([]string, 0, len(ordered))
	for _, c := range ordered {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		unique = append(unique, trimmed)
	}

	type probeRes struct {
		idx    int
		metric MultipathPathMetric
		ok     bool
	}
	ch := make(chan probeRes, len(unique))
	var wg sync.WaitGroup
	for i, addr := range unique {
		wg.Add(1)
		go func(index int, address string) {
			defer wg.Done()
			start := time.Now()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", address)
			rtt := time.Since(start)
			if err != nil {
				ch <- probeRes{idx: index, ok: false}
				return
			}
			_ = conn.Close()
			ch <- probeRes{
				idx: index,
				ok:  true,
				metric: MultipathPathMetric{
					Candidate: address,
					LinkType:  classifyCandidateLink(address),
					RTT:       rtt,
				},
			}
		}(i, addr)
	}
	wg.Wait()
	close(ch)

	var reachable []MultipathPathMetric
	for r := range ch {
		if r.ok {
			reachable = append(reachable, r.metric)
		}
	}
	local := localPrivateIPv4()
	tier := func(cand string) int {
		ip := parseCandidateIP(cand)
		if ip == nil {
			return 0
		}
		if sharesSubnetWith(ip, local) || ip.IsLoopback() {
			return 2
		}
		if isCGNATIP(ip) {
			return 1
		}
		return 0
	}
	sort.SliceStable(reachable, func(i, j int) bool {
		ti, tj := tier(reachable[i].Candidate), tier(reachable[j].Candidate)
		if ti != tj {
			return ti > tj
		}
		return reachable[i].RTT < reachable[j].RTT
	})
	return reachable
}

// ServeMultipath 在监听端口上并发接受多条 TCP 分片连接（有线 + Wi-Fi + Tailscale），
// 服务各连接请求的 [ResumeOffset, EndOffset) 1 MiB Chunk 直到全文件字节交付完毕（v7.3.0）。
func (s *Sender) ServeMultipath(ctx context.Context, progress func(bytesDone, speedBps int64)) error {
	return ServeMultipathListeners(ctx, s.cfg, []net.Listener{s.listener}, progress)
}

// ServeMultipathListeners 支持在多个网卡 Listener 上同时接受并发分片拉取连接，完成多路径聚合发送（v7.3.0）。
func ServeMultipathListeners(ctx context.Context, cfg SendConfig, listeners []net.Listener, progress func(bytesDone, speedBps int64)) error {
	if cfg.File == nil || cfg.Token == "" || len(listeners) == 0 {
		return errors.New("transfer: file, token, and listeners are required")
	}
	totalChunks := int((cfg.Size + int64(ChunkSize) - 1) / int64(ChunkSize))
	if totalChunks <= 0 {
		totalChunks = 1
	}

	var (
		mu          sync.Mutex
		doneChunks  = make([]bool, totalChunks)
		doneCount   int
		bytesDone   int64
		allDoneOnce sync.Once
		allDoneCh   = make(chan struct{})
		firstErr    error
		start       = time.Now()
		lastReport  = start
		lastBytes   int64
	)

	markRangeCompleted := func(startOff, endOff int64) {
		mu.Lock()
		defer mu.Unlock()
		if startOff == 0 && endOff >= cfg.Size {
			bytesDone = cfg.Size
			doneCount = totalChunks
		} else {
			firstIdx := int(startOff / int64(ChunkSize))
			lastIdx := int((endOff - 1) / int64(ChunkSize))
			for idx := firstIdx; idx <= lastIdx && idx < totalChunks; idx++ {
				if !doneChunks[idx] {
					doneChunks[idx] = true
					doneCount++
					cStart := int64(idx) * int64(ChunkSize)
					cEnd := cStart + int64(ChunkSize)
					if cEnd > cfg.Size {
						cEnd = cfg.Size
					}
					bytesDone += cEnd - cStart
				}
			}
		}
		if progress != nil {
			now := time.Now()
			if now.Sub(lastReport) >= progressInterval || bytesDone >= cfg.Size {
				elapsed := now.Sub(lastReport).Seconds()
				var speed int64
				if elapsed > 0 {
					speed = int64(float64(bytesDone-lastBytes) / elapsed)
				}
				progress(bytesDone, speed)
				lastReport = now
				lastBytes = bytesDone
			}
		}
		if doneCount >= totalChunks || bytesDone >= cfg.Size {
			allDoneOnce.Do(func() { close(allDoneCh) })
		}
	}

	var connWG sync.WaitGroup
	acceptConn := func(ln net.Listener) {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 若是探测连接（不发 HELLO 直接关闭），serveSendConn 读 EOF 会安静退出。
			connWG.Add(1)
			go func(conn net.Conn) {
				defer connWG.Done()
				defer conn.Close()
				sStart, sEnd, err := serveMultipathConn(ctx, cfg, conn)
				if err != nil {
					if errors.Is(err, ErrCanceled) || errors.Is(err, ErrBadToken) {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						allDoneOnce.Do(func() { close(allDoneCh) })
					}
					return
				}
				markRangeCompleted(sStart, sEnd)
			}(c)
		}
	}

	for _, ln := range listeners {
		go acceptConn(ln)
	}

	select {
	case <-allDoneCh:
	case <-ctx.Done():
		for _, ln := range listeners {
			_ = ln.Close()
		}
		connWG.Wait()
		return ctx.Err()
	}

	for _, ln := range listeners {
		_ = ln.Close()
	}
	connWG.Wait()

	mu.Lock()
	err := firstErr
	mu.Unlock()
	return err
}

func serveMultipathConn(ctx context.Context, cfg SendConfig, conn net.Conn) (int64, int64, error) {
	fr := protocol.NewFramer(conn)
	hello, err := readHello(fr)
	if err != nil {
		return 0, 0, err
	}
	if hello.Token != cfg.Token {
		_ = writePeerError(fr, "bad token")
		return 0, 0, ErrBadToken
	}
	resume := hello.ResumeOffset
	if resume < 0 || resume > cfg.Size {
		_ = writePeerError(fr, "invalid resume offset")
		return 0, 0, fmt.Errorf("%w: resumeOffset %d", errBadPayload, resume)
	}
	endOffset := cfg.Size
	if hello.EndOffset > 0 {
		if hello.EndOffset <= resume || hello.EndOffset > cfg.Size {
			_ = writePeerError(fr, "invalid end offset")
			return 0, 0, fmt.Errorf("%w: endOffset %d", errBadPayload, hello.EndOffset)
		}
		endOffset = hello.EndOffset
	}
	if err := writeAccept(fr, cfg.Size, ChunkSize); err != nil {
		return 0, 0, err
	}

	cw := newCreditWindow(MaxCreditBytes)
	cw.sent = resume
	cw.acked = resume
	buf := make([]byte, ChunkSize)

	ackCh := make(chan ackResult, 8)
	ackCtx, stopAck := context.WithCancel(ctx)
	defer stopAck()
	go readAcks(ackCtx, fr, ackCh)

	bytesSent := resume
	for bytesSent < endOffset {
		if err := ctx.Err(); err != nil {
			_ = writeCancel(fr)
			return 0, 0, ErrCanceled
		}
		readLen := len(buf)
		if rem := endOffset - bytesSent; int64(readLen) > rem {
			readLen = int(rem)
		}
		n, err := cfg.File.ReadAt(buf[:readLen], bytesSent)
		if err != nil && n == 0 {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, 0, err
		}
		for sent := 0; sent < n; {
			take := cw.acquire(n - sent)
			if take == 0 {
				select {
				case r := <-ackCh:
					if r.err != nil {
						if errors.Is(r.err, ErrCanceled) {
							return 0, 0, ErrCanceled
						}
						return 0, 0, r.err
					}
					cw.grant(r.cumOffset, int64(r.credit))
				case <-ctx.Done():
					_ = writeCancel(fr)
					return 0, 0, ErrCanceled
				}
				continue
			}
			if err := throttleBytes(ctx, take); err != nil {
				_ = writeCancel(fr)
				return 0, 0, ErrCanceled
			}
			if err := writeData(fr, bytesSent+int64(sent), buf[sent:sent+take]); err != nil {
				return 0, 0, err
			}
			sent += take
			bytesSent += int64(take)
		}
	}

	if err := writeFin(fr, cfg.SHA256, bytesSent); err != nil {
		return 0, 0, err
	}
	if cwConn, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cwConn.CloseWrite()
	}
	grace := time.NewTimer(2 * time.Second)
	defer grace.Stop()
	for {
		select {
		case r := <-ackCh:
			if r.err != nil {
				return resume, endOffset, nil
			}
			cw.grant(r.cumOffset, int64(r.credit))
		case <-grace.C:
			return resume, endOffset, nil
		case <-ctx.Done():
			return resume, endOffset, nil
		}
	}
}

// ReceiveMultipath 执行多路径并发分片拉取（Multipath Chunk Scheduling，v7.3.0）：
//  1. 探测并排序有线局域网、Wi-Fi 与 Tailscale 候选地址（或直接并发拨号）；
//  2. 将待传文件按 1 MiB Chunk 切分到动态工作队列；
//  3. 多条 TCP 连接按实时 RTT 与吞吐并发拉取不同 1 MiB Chunk 并通过 WriteAt 写入 .part；
//  4. 当任一链路闪断或超时时，未完成 Chunk 自动重回队列由其余健康链路零中断接管；
//  5. 全部 Chunk 写满后执行全文件 SHA-256 校验并原子重命名落盘。
func ReceiveMultipath(ctx context.Context, cfg ReceiveConfig, progress func(bytesDone, speedBps int64)) (*MultipathStats, error) {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 3 * time.Second
	}
	if len(cfg.Candidates) == 0 {
		return nil, ErrDialFailed
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DestPath), 0o755); err != nil {
		return nil, err
	}
	partPath := cfg.DestPath + ".part"
	metaPath := cfg.DestPath + ".meta"

	resume, err := recoverOffset(metaPath, partPath, &cfg)
	if err != nil {
		return nil, err
	}
	// 对按块并发乱序写入的续传，向下对齐到 ChunkSize 边界。
	resume = (resume / int64(ChunkSize)) * int64(ChunkSize)

	part, err := os.OpenFile(partPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
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
		return nil, err
	}

	// 构建候选路径表（不去重端口相同但 IP 不同的多网卡地址，保留全部可用候选）。
	candidates := preferLocalSubnet(append([]string(nil), cfg.Candidates...))
	routes := make([]MultipathPathMetric, 0, len(candidates))
	seenCand := map[string]bool{}
	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" || seenCand[trimmed] {
			continue
		}
		seenCand[trimmed] = true
		routes = append(routes, MultipathPathMetric{
			Candidate: trimmed,
			LinkType:  classifyCandidateLink(trimmed),
		})
	}
	if len(routes) == 0 {
		return nil, ErrDialFailed
	}

	// 将 [resume, cfg.Size) 切分为 1 MiB Chunk 队列。
	var chunks []multipathChunk
	for off := resume; off < cfg.Size; off += int64(ChunkSize) {
		end := off + int64(ChunkSize)
		if end > cfg.Size {
			end = cfg.Size
		}
		chunks = append(chunks, multipathChunk{
			index: len(chunks),
			start: off,
			end:   end,
		})
	}
	totalChunks := len(chunks)

	var (
		qMu           sync.Mutex
		pending       = append([]multipathChunk(nil), chunks...)
		completedCnt  int
		bytesDone     = resume
		failoverCount atomic.Int32
		anyDialOK     atomic.Bool
		fatalErr      error
		lastReport    = time.Now()
		lastBytes     = resume
	)

	popChunk := func() (multipathChunk, bool) {
		qMu.Lock()
		defer qMu.Unlock()
		if fatalErr != nil || len(pending) == 0 {
			return multipathChunk{}, false
		}
		ch := pending[0]
		pending = pending[1:]
		return ch, true
	}

	requeueChunk := func(ch multipathChunk) {
		qMu.Lock()
		defer qMu.Unlock()
		ch.tries++
		pending = append(pending, ch)
	}

	markChunkDone := func(routeIdx int, ch multipathChunk, elapsed time.Duration) {
		qMu.Lock()
		defer qMu.Unlock()
		dataStarted = true
		chunkBytes := ch.end - ch.start
		completedCnt++
		bytesDone += chunkBytes
		routes[routeIdx].ChunksDone++
		routes[routeIdx].BytesDone += chunkBytes
		if elapsed > 0 {
			bps := int64(float64(chunkBytes) / elapsed.Seconds())
			if routes[routeIdx].ThroughputBps == 0 {
				routes[routeIdx].ThroughputBps = bps
			} else {
				routes[routeIdx].ThroughputBps = (routes[routeIdx].ThroughputBps*3 + bps) / 4
			}
			routes[routeIdx].RTT = elapsed
		}
		// 仅当全部块完成时将 meta.BytesDone 推到 cfg.Size，部分完成时保持保守偏移。
		if completedCnt == totalChunks {
			_ = mw.update(cfg.Size, true)
		}
		if progress != nil {
			now := time.Now()
			if now.Sub(lastReport) >= progressInterval || bytesDone == cfg.Size {
				dt := now.Sub(lastReport).Seconds()
				var speed int64
				if dt > 0 {
					speed = int64(float64(bytesDone-lastBytes) / dt)
				}
				progress(bytesDone, speed)
				lastReport = now
				lastBytes = bytesDone
			}
		}
	}

	isAllCompleted := func() bool {
		qMu.Lock()
		defer qMu.Unlock()
		return completedCnt >= totalChunks || fatalErr != nil
	}

	var workerWG sync.WaitGroup
	for idx := range routes {
		workerWG.Add(1)
		go func(routeIdx int) {
			defer workerWG.Done()
			addr := routes[routeIdx].Candidate
			for {
				if ctx.Err() != nil {
					qMu.Lock()
					userCanceled = true
					if fatalErr == nil {
						fatalErr = ErrCanceled
					}
					qMu.Unlock()
					return
				}
				if isAllCompleted() {
					return
				}
				ch, ok := popChunk()
				if !ok {
					// 若仍有其他 worker 在处理最后的在途块（且可能失败重入队），短暂等待。
					time.Sleep(10 * time.Millisecond)
					if isAllCompleted() {
						return
					}
					continue
				}

				start := time.Now()
				err := pullChunkOnPath(ctx, cfg, addr, part, ch)
				if err != nil {
					if errors.Is(err, ErrCanceled) || errors.Is(err, ErrBadToken) {
						qMu.Lock()
						if errors.Is(err, ErrCanceled) {
							userCanceled = true
						}
						if fatalErr == nil {
							fatalErr = err
						}
						qMu.Unlock()
						return
					}
					qMu.Lock()
					routes[routeIdx].FailedCount++
					qMu.Unlock()
					failoverCount.Add(1)
					requeueChunk(ch)
					// 故障路径退避，让健康路径优先拉取重入队的 Chunk。
					if routes[routeIdx].FailedCount >= 3 {
						return
					}
					time.Sleep(25 * time.Millisecond)
					continue
				}
				anyDialOK.Store(true)
				markChunkDone(routeIdx, ch, time.Since(start))
			}
		}(idx)
	}
	workerWG.Wait()

	qMu.Lock()
	fErr := fatalErr
	doneAll := completedCnt >= totalChunks
	qMu.Unlock()

	if fErr != nil {
		return nil, fErr
	}
	if !anyDialOK.Load() && totalChunks > 0 {
		return nil, ErrDialFailed
	}
	if !doneAll {
		_ = mw.flush()
		return nil, io.ErrUnexpectedEOF
	}

	// 全文件 SHA-256 校验。
	hasher := sha256.New()
	if _, err := part.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.CopyN(hasher, part, cfg.Size); err != nil {
		return nil, err
	}
	if gotSum := hex.EncodeToString(hasher.Sum(nil)); gotSum != cfg.SHA256 {
		return nil, ErrChecksum
	}
	if err := part.Sync(); err != nil {
		return nil, err
	}
	if err := part.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(partPath, cfg.DestPath); err != nil {
		return nil, err
	}
	_ = os.Remove(metaPath)

	pathsUsed := 0
	for _, r := range routes {
		if r.ChunksDone > 0 {
			pathsUsed++
		}
	}
	return &MultipathStats{
		PathsUsed:     pathsUsed,
		TotalChunks:   totalChunks,
		FailoverCount: int(failoverCount.Load()),
		Routes:        routes,
	}, nil
}

func pullChunkOnPath(ctx context.Context, cfg ReceiveConfig, addr string, part *os.File, ch multipathChunk) error {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	fr := protocol.NewFramer(conn)
	if err := writeHelloRange(fr, cfg.Token, ch.start, ch.end); err != nil {
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

	received := ch.start
	lastAck := ch.start
	for received < ch.end {
		if err := ctx.Err(); err != nil {
			_ = writeCancel(fr)
			return ErrCanceled
		}
		offset, data, err := readData(fr)
		if err != nil {
			return err
		}
		if offset != received {
			_ = writePeerError(fr, fmt.Sprintf("non-contiguous offset %d want %d", offset, received))
			return fmt.Errorf("%w: offset %d want %d", errBadPayload, offset, received)
		}
		if _, err := part.WriteAt(data, received); err != nil {
			return err
		}
		received += int64(len(data))
		if received-lastAck >= MinCreditBytes || received == ch.end {
			if err := writeAck(fr, received, MaxCreditBytes); err != nil {
				return err
			}
			lastAck = received
		}
	}

	fin, err := readFin(fr)
	if err != nil {
		return err
	}
	if fin.TotalBytes != ch.end {
		return fmt.Errorf("%w: fin total %d want %d", errBadPayload, fin.TotalBytes, ch.end)
	}
	return nil
}
