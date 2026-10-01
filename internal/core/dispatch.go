package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// readPump 常驻读取并分发信封，连接关闭 / 出错即返回。
func (c *Client) readPump(gen uint64, conn *websocket.Conn) error {
	for {
		if c.runCtx.Err() != nil {
			return c.runCtx.Err()
		}
		env, err := readEnvelope(c.runCtx, conn)
		if err != nil {
			return err
		}
		c.dispatch(gen, env)
	}
}

// dispatch 按类型路由：在线表 / 消息本地处理，文件与中继信令投递到任务。
func (c *Client) dispatch(gen uint64, env *protocol.Envelope) {
	switch env.Type {
	case protocol.UserList:
		c.applyUserList(env)
	case protocol.UserJoin:
		c.applyJoin(env)
	case protocol.UserLeave:
		c.applyLeave(env)
	case protocol.PresenceUpdate:
		c.applyPresence(env)

	case protocol.TextMsg:
		var p protocol.TextPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		c.listener().OnMessageReceived(env.From, env.Group, env.ID, protocol.TextMsg, p.Text)

	case protocol.StickerMsg:
		var p protocol.StickerPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		// M4 表情以名称引用（UI 本地缓存贴纸资源）；b64 缺省时不展开。
		c.listener().OnMessageReceived(env.From, env.Group, env.ID, protocol.StickerMsg, p.Name)

	case protocol.ServerShutdown:
		c.mu.Lock()
		c.connState = "disconnected"
		c.mu.Unlock()
		c.listener().OnConnChanged("disconnected", "server shutdown")

	case protocol.ChannelList:
		var p protocol.ChannelListPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		c.applyChannelList(p.Channels)

	case protocol.GroupOffer:
		c.offerGroupInbound(env)
	case protocol.GroupJoin:
		c.routeGroupSignal(env, evJoin)
	case protocol.GroupLeave:
		c.routeGroupSignal(env, evLeave)
	case protocol.GroupProgress:
		c.routeGroupSignal(env, evProgress)

	case protocol.AuthFail:
		// 握手后收到鉴权失败按断连处理（watch 会决定重连策略）。
		c.mu.Lock()
		c.connState = "disconnected"
		c.mu.Unlock()

	case protocol.Error:
		c.routeError(env)

	case protocol.MsgAck:
		c.routeAck(env)

	default:
		if isTransferSignal(env.Type) {
			c.routeToTransfer(env)
		}
	}
}

// isTransferSignal 判定是否为应路由到任务的文件 / 中继信令。
func isTransferSignal(typ string) bool {
	switch typ {
	case protocol.FileOffer,
		protocol.FileAccept,
		protocol.FileReject,
		protocol.FileCancel,
		protocol.FileReverse,
		protocol.FileDone,
		protocol.RelayGrant,
		protocol.RelayKey:
		return true
	}
	return false
}

// routeToTransfer 从负载提取 transferID 并投递到对应任务。
func (c *Client) routeToTransfer(env *protocol.Envelope) {
	id := transferIDOf(env)
	if id == "" {
		return
	}
	t, ok := c.getTransfer(id)
	if !ok {
		// 新的 FILE_OFFER：尚无任务，由 offerInbound 建入站待响应任务。
		if env.Type == protocol.FileOffer {
			c.offerInbound(env)
		}
		return
	}
	t.deliver(taskSignal{typ: env.Type, env: env})
}

// transferIDOf 提取各类任务信令的 transferID。
func transferIDOf(env *protocol.Envelope) string {
	switch env.Type {
	case protocol.FileOffer:
		var p protocol.FileOfferPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.FileAccept:
		var p protocol.FileAcceptPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.FileReject:
		var p protocol.FileRejectPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.FileCancel:
		var p protocol.FileCancelPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.FileReverse:
		var p protocol.FileReversePayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.FileDone:
		var p protocol.FileDonePayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.RelayGrant:
		var p protocol.RelayGrantPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	case protocol.RelayKey:
		var p protocol.RelayKeyPayload
		_ = env.DecodePayload(&p)
		return p.TransferID
	}
	return ""
}

// routeError 把 ERROR 投递给发起请求的任务（RELAY_REQUEST 的错误）。
func (c *Client) routeError(env *protocol.Envelope) {
	c.mu.Lock()
	for _, t := range c.transfers {
		t.mu.Lock()
		match := t.relayRequest != "" && t.relayRequest == env.ReplyTo
		t.mu.Unlock()
		if match {
			t.deliver(taskSignal{typ: protocol.Error, env: env})
			break
		}
	}
	c.mu.Unlock()
}

// routeAck 把 undeliverable 等 ACK 投递给发起 RELAY_REQUEST 的任务。
func (c *Client) routeAck(env *protocol.Envelope) {
	c.mu.Lock()
	for _, t := range c.transfers {
		t.mu.Lock()
		match := t.relayRequest != "" && t.relayRequest == env.ReplyTo
		t.mu.Unlock()
		if match {
			t.deliver(taskSignal{typ: protocol.MsgAck, env: env})
			break
		}
	}
	c.mu.Unlock()
}

// fmtErrAuth 构造鉴权失败哨兵（携带服务器原因）。
func fmtErrAuth(reason string) error {
	if reason == "" {
		return errAuthFailed
	}
	return fmt.Errorf("%w: %s", errAuthFailed, reason)
}

var (
	_ = context.Background
	_ = errors.New
)

// routeGroupSignal 把 GROUP_JOIN/LEAVE/PROGRESS 投递到对应群组任务。
func (c *Client) routeGroupSignal(env *protocol.Envelope, kind groupEventKind) {
	var transferID string
	switch kind {
	case evProgress:
		var p protocol.GroupProgressPayload
		if env.DecodePayload(&p) != nil {
			return
		}
		transferID = p.TransferID
	default:
		var p protocol.GroupJoinPayload
		if env.DecodePayload(&p) != nil {
			return
		}
		transferID = p.TransferID
	}
	t, ok := c.getGroupTask(transferID)
	if !ok {
		return
	}
	t.post(groupEvent{kind: kind, from: env.From, env: env})
}
