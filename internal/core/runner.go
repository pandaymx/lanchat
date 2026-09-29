package core

import (
	"context"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// 数据路径模式，写入 transferTask.mode；切换路径时据此丢弃过期结果。
const (
	modeDirect = 1 + iota
	modeReverse
	modeRelay
)

// awaitFor 的返回状态。
const (
	statusOK = iota
	statusTimeout
	statusCanceled
)

// relayWaitTimeout 是发送方在直连失败后等待对端（接收方）发起中继的上限。
// 约定 RELAY_REQUEST 只由接收方发起，避免双方各建一个中继会话。
const relayWaitTimeout = 10 * time.Second

// pathOutcome 是一条数据路径的结束结果：sig 非空表示被信令打断，err 为路径错误。
type pathOutcome struct {
	sig *taskSignal
	err error
}

func (o pathOutcome) succeeded() bool { return o.sig == nil && o.err == nil }

func (o pathOutcome) peerStopped() bool {
	return o.sig != nil && (o.sig.typ == protocol.FileCancel || o.sig.typ == protocol.FileReject)
}

// runPath 启动一条数据路径并等待其结束；期间收到控制信令则取消路径并回送信令，
// 保证数据 goroutine 不泄漏，且过期路径的结果不会污染新模式。
func (c *Client) runPath(t *transferTask, mode int, fn func(context.Context) error) pathOutcome {
	t.mu.Lock()
	t.mode = mode
	t.mu.Unlock()

	// 清理上一条路径的残留结果；属于本次模式的结果（竞态先到）则直接采用。
	select {
	case r := <-t.dataCh:
		if r.mode == mode {
			return pathOutcome{err: r.err}
		}
	default:
	}

	pathCtx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	go func() {
		t.dataCh <- dataResult{mode: mode, err: fn(pathCtx)}
	}()

	for {
		select {
		case r := <-t.dataCh:
			if r.mode != mode {
				continue
			}
			return pathOutcome{err: r.err}
		case s := <-t.ch:
			if isControl(s.typ) {
				cancel()
				// 等待本次路径 goroutine 退出：启动前已排空 dataCh，且只有它写入。
				<-t.dataCh
				return pathOutcome{sig: &s}
			}
			// 非控制信令（如重复 offer）忽略。
		case <-t.ctx.Done():
			cancel()
			<-t.dataCh
			return pathOutcome{err: t.ctx.Err()}
		}
	}
}

// isControl 判定是否应中断当前数据路径的信令。
func isControl(typ string) bool {
	switch typ {
	case protocol.FileCancel,
		protocol.FileReject,
		protocol.FileReverse,
		protocol.RelayGrant,
		protocol.RelayKey,
		protocol.Error,
		protocol.MsgAck:
		return true
	}
	return false
}

// await 等待指定类型信令之一；FILE_CANCEL / FILE_REJECT 始终中断。
func (c *Client) await(t *transferTask, types ...string) (*taskSignal, int) {
	return c.awaitFor(t, 0, types...)
}

// awaitFor 等待指定类型信令，timeout>0 时超时返回 statusTimeout；t.ctx 取消返回 statusCanceled。
func (c *Client) awaitFor(t *transferTask, timeout time.Duration, types ...string) (*taskSignal, int) {
	want := make(map[string]bool, len(types))
	for _, x := range types {
		want[x] = true
	}
	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		timerCh = timer.C
		defer timer.Stop()
	}
	for {
		select {
		case s := <-t.ch:
			if want[s.typ] || s.typ == protocol.FileCancel || s.typ == protocol.FileReject {
				sp := s
				return &sp, statusOK
			}
		case <-timerCh:
			return nil, statusTimeout
		case <-t.ctx.Done():
			return nil, statusCanceled
		}
	}
}

// abortCanceled 处理 t.ctx 取消：Close 静默返回；会话中断（断线）则判任务失败。
// 返回 true 表示 runner 应结束。
func (c *Client) abortCanceled(t *transferTask) bool {
	if c.runCtx.Err() != nil {
		return true
	}
	t.fail("connection lost")
	return true
}

// finishOrSwitch 处理路径结束：成功 / 对端终止 / 被取消返回 true；其余（失败）返回 false。
func (c *Client) finishOrSwitch(t *transferTask, out pathOutcome) bool {
	if out.succeeded() {
		t.done()
		return true
	}
	if out.peerStopped() {
		t.canceled("peer canceled")
		return true
	}
	if out.err != nil && errors.Is(out.err, context.Canceled) {
		return c.abortCanceled(t)
	}
	return false
}

// runOutbound 出站任务：监听 P2P → FILE_OFFER → 按「直连 → 反向 → 中继」推进。
func (c *Client) runOutbound(t *transferTask, conn *websocket.Conn) {
	// 生成的一次性 token 必须落到 t.token：relayConfig 据此在中继路径上保持与接收方一致。
	t.token = uuid.NewString()
	cfg := transfer.SendConfig{
		TransferID: t.snap.ID,
		File:       t.file,
		Name:       t.snap.Name,
		Size:       t.snap.Size,
		SHA256:     t.sha256,
		Token:      t.token,
	}
	sender, err := transfer.NewSender(cfg)
	if err != nil {
		t.fail(errString(err))
		return
	}
	defer sender.Close()
	defer func() { _ = t.file.Close() }()

	if _, err := c.sendTo(conn, protocol.FileOffer, t.snap.PeerID, protocol.FileOfferPayload{
		TransferID: t.snap.ID,
		Name:       t.snap.Name,
		Size:       t.snap.Size,
		ChunkSize:  transfer.ChunkSize,
		SHA256:     t.sha256,
		Candidates: sender.CandidateAddrs(),
		Token:      t.token,
		ECDHPub:    transfer.EncodePub(t.senderPriv.PublicKey()),
	}); err != nil {
		t.fail(errString(err))
		return
	}

	// 等接收方接受 / 拒绝。
	sig, st := c.await(t, protocol.FileAccept)
	if st == statusCanceled {
		_ = c.abortCanceled(t)
		return
	}
	switch sig.typ {
	case protocol.FileReject:
		t.canceled("peer rejected")
		return
	case protocol.FileCancel:
		t.canceled("peer canceled")
		return
	case protocol.FileAccept:
	}

	out := c.runPath(t, modeDirect, func(ctx context.Context) error {
		return sender.Serve(ctx, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	if out.sig != nil {
		switch out.sig.typ {
		case protocol.FileReverse:
			c.outboundReverse(t, cfg, out.sig.env)
			return
		case protocol.RelayGrant:
			var g protocol.RelayGrantPayload
			_ = out.sig.env.DecodePayload(&g)
			c.outboundRelayGrant(t, g)
			return
		}
	}

	// Serve 自身报错且无切换信令：给接收方一点时间发 FILE_REVERSE / RELAY_GRANT。
	sig, st = c.awaitFor(t, relayWaitTimeout, protocol.FileReverse, protocol.RelayGrant)
	switch st {
	case statusCanceled:
		_ = c.abortCanceled(t)
	case statusTimeout:
		t.fail("peer unreachable")
	case statusOK:
		switch sig.typ {
		case protocol.FileCancel, protocol.FileReject:
			t.canceled("peer canceled")
		case protocol.FileReverse:
			c.outboundReverse(t, cfg, sig.env)
		case protocol.RelayGrant:
			var g protocol.RelayGrantPayload
			_ = sig.env.DecodePayload(&g)
			c.outboundRelayGrant(t, g)
		}
	}
}

// outboundReverse 发送方按接收方回传的候选反向拨号。
func (c *Client) outboundReverse(t *transferTask, cfg transfer.SendConfig, env *protocol.Envelope) {
	var p protocol.FileReversePayload
	if err := env.DecodePayload(&p); err != nil {
		t.fail(errString(err))
		return
	}
	if p.Token != "" {
		cfg.Token = p.Token
	}
	out := c.runPath(t, modeReverse, func(ctx context.Context) error {
		return transfer.DialSend(ctx, cfg, p.Candidates, directTryTimeout, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	if out.sig != nil {
		var g protocol.RelayGrantPayload
		if out.sig.typ == protocol.RelayGrant {
			_ = out.sig.env.DecodePayload(&g)
			c.outboundRelayGrant(t, g)
			return
		}
	}
	t.fail(errString(out.err))
}

// outboundRelayGrant 发送方收到 RELAY_GRANT：等接收方回 RELAY_KEY，解出文件密钥后经中继发送。
func (c *Client) outboundRelayGrant(t *transferTask, g protocol.RelayGrantPayload) {
	if g.RelayAddr == "" {
		t.fail("relay address unavailable")
		return
	}
	sig, st := c.await(t, protocol.RelayKey)
	if st == statusCanceled {
		_ = c.abortCanceled(t)
		return
	}
	if sig.typ != protocol.RelayKey {
		t.canceled("peer canceled")
		return
	}

	var k protocol.RelayKeyPayload
	if err := sig.env.DecodePayload(&k); err != nil {
		t.fail(errString(err))
		return
	}
	peerPub, err := transfer.DecodePub(k.PeerPub)
	if err != nil {
		t.fail(errString(err))
		return
	}
	kek, err := transfer.DeriveKEK(t.senderPriv, peerPub)
	if err != nil {
		t.fail(errString(err))
		return
	}
	fileKey, err := transfer.UnwrapKey(kek, k.WrappedKey)
	if err != nil {
		t.fail(errString(err))
		return
	}

	rc := relayConfig(t, g, fileKey)
	out := c.runPath(t, modeRelay, func(ctx context.Context) error {
		return transfer.RelaySend(ctx, rc, t.file, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	t.fail(errString(out.err))
}

// runInbound 入站任务：按「直连拨号 → 监听反向 → 中继」推进。
func (c *Client) runInbound(t *transferTask, conn *websocket.Conn, destPath string) {
	t.mu.Lock()
	t.destPath = destPath
	t.mu.Unlock()
	cfg := transfer.ReceiveConfig{
		TransferID:  t.snap.ID,
		Token:       t.token,
		DestPath:    destPath,
		Candidates:  t.candidates,
		Size:        t.snap.Size,
		SHA256:      t.sha256,
		DialTimeout: directTryTimeout,
	}

	out := c.runPath(t, modeDirect, func(ctx context.Context) error {
		return transfer.Receive(ctx, cfg, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	if out.sig != nil && out.sig.typ == protocol.RelayGrant {
		var g protocol.RelayGrantPayload
		_ = out.sig.env.DecodePayload(&g)
		c.inboundRelayGrant(t, conn, g)
		return
	}

	// 直连失败：本端监听，回 FILE_REVERSE 让发送方拨入。
	receiver, err := transfer.NewReceiver(cfg)
	if err != nil {
		t.fail(errString(err))
		return
	}
	defer receiver.Close()
	if _, err := c.sendTo(conn, protocol.FileReverse, t.snap.PeerID, protocol.FileReversePayload{
		TransferID: t.snap.ID,
		Candidates: receiver.CandidateAddrs(),
		Token:      t.token,
	}); err != nil {
		t.fail(errString(err))
		return
	}

	out = c.runPath(t, modeReverse, func(ctx context.Context) error {
		return receiver.Serve(ctx, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	if out.sig != nil && out.sig.typ == protocol.RelayGrant {
		var g protocol.RelayGrantPayload
		_ = out.sig.env.DecodePayload(&g)
		c.inboundRelayGrant(t, conn, g)
		return
	}

	// 反向仍失败：由接收方发起 RELAY_REQUEST（唯一发起方）。
	if err := c.inboundRelayRequest(t, conn); err != nil {
		if errors.Is(err, context.Canceled) {
			_ = c.abortCanceled(t)
			return
		}
		t.fail(errString(err))
	}
}

// inboundRelayRequest 接收方发起中继请求，收到 grant 后生成并回传文件密钥。
func (c *Client) inboundRelayRequest(t *transferTask, conn *websocket.Conn) error {
	req, err := c.sendTo(conn, protocol.RelayRequest, "", protocol.RelayRequestPayload{
		TransferID: t.snap.ID,
		PeerID:     t.snap.PeerID,
	})
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.relayRequest = req.ID
	t.mu.Unlock()

	sig, st := c.await(t, protocol.RelayGrant, protocol.Error, protocol.MsgAck)
	if st == statusCanceled {
		return context.Canceled
	}
	switch sig.typ {
	case protocol.Error:
		return payloadError(sig.env)
	case protocol.MsgAck:
		return errors.New("relay unavailable: peer offline")
	case protocol.FileCancel, protocol.FileReject:
		t.canceled("peer canceled")
		return nil
	}
	var g protocol.RelayGrantPayload
	if err := sig.env.DecodePayload(&g); err != nil {
		return err
	}
	c.inboundRelayGrant(t, conn, g)
	return nil
}

// inboundRelayGrant 接收方生成临时 X25519 + 文件密钥，封装回 RELAY_KEY，然后经中继接收。
func (c *Client) inboundRelayGrant(t *transferTask, conn *websocket.Conn, g protocol.RelayGrantPayload) {
	if g.RelayAddr == "" {
		t.fail("relay address unavailable")
		return
	}
	senderPub, err := transfer.DecodePub(t.senderECDH)
	if err != nil {
		t.fail(errString(err))
		return
	}
	recvPriv, err := transfer.GenerateX25519()
	if err != nil {
		t.fail(errString(err))
		return
	}
	fileKey, err := transfer.GenerateFileKey()
	if err != nil {
		t.fail(errString(err))
		return
	}
	kek, err := transfer.DeriveKEK(recvPriv, senderPub)
	if err != nil {
		t.fail(errString(err))
		return
	}
	wrapped, err := transfer.WrapKey(kek, fileKey)
	if err != nil {
		t.fail(errString(err))
		return
	}
	if _, err := c.sendTo(conn, protocol.RelayKey, t.snap.PeerID, protocol.RelayKeyPayload{
		RelayID:    g.RelayID,
		TransferID: t.snap.ID,
		PeerPub:    transfer.EncodePub(recvPriv.PublicKey()),
		WrappedKey: wrapped,
	}); err != nil {
		t.fail(errString(err))
		return
	}

	rc := relayConfig(t, g, fileKey)
	out := c.runPath(t, modeRelay, func(ctx context.Context) error {
		return transfer.RelayReceive(ctx, rc, t.destPath, c.progressFn(t))
	})
	if c.finishOrSwitch(t, out) {
		return
	}
	t.fail(errString(out.err))
}

// relayConfig 组装收发共用的中继参数。
func relayConfig(t *transferTask, g protocol.RelayGrantPayload, fileKey []byte) transfer.RelayConfig {
	return transfer.RelayConfig{
		RelayAddr:  g.RelayAddr,
		RelayID:    g.RelayID,
		FileKey:    fileKey,
		TransferID: t.snap.ID,
		Token:      t.token,
		Name:       t.snap.Name,
		Size:       t.snap.Size,
		SHA256:     t.sha256,
	}
}

// payloadError 把 ERROR 信封解码为普通错误。
func payloadError(env *protocol.Envelope) error {
	var p protocol.ErrorPayload
	if err := env.DecodePayload(&p); err != nil {
		return err
	}
	if p.Message != "" {
		return errors.New(p.Message)
	}
	return errors.New(p.Code)
}
