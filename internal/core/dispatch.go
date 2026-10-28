package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
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
		if env.Type == protocol.ServerShutdown {
			return errors.New("server shutdown")
		}
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
		text := p.Text
		var pendingChannelCipher string
		if strings.HasPrefix(text, transfer.TextE2EEPrefix) {
			c.mu.Lock()
			e2eeSecret := c.e2eeKey
			c.mu.Unlock()
			if plain, err := transfer.OpenText(e2eeSecret, text); err == nil {
				text = plain
			} else {
				text = "[加密消息] 需配置相同 /e2ee 密钥解密"
			}
		} else if strings.HasPrefix(text, transfer.TextECDHPrefix) {
			c.mu.Lock()
			idPriv := c.identityPriv
			senderPub := c.peerPubs[env.From]
			if senderPub == "" && p.SenderPub != "" {
				senderPub = p.SenderPub
				c.peerPubs[env.From] = senderPub
			}
			c.mu.Unlock()
			if plain, err := transfer.OpenECDHText(idPriv, senderPub, text); err == nil {
				text = plain
			} else {
				text = "[加密消息] 无法验证对端 X25519 密钥"
			}
		} else if strings.HasPrefix(text, transfer.TextChannelPrefix) {
			chKey := c.ensureChannelKey(env.Group)
			if plain, err := transfer.OpenChannelText(chKey, text); err == nil {
				text = plain
			} else {
				pendingChannelCipher = text
				text = "[频道加密消息] 正在同步频道密钥..."
			}
		}

		// 处理控制类指令（M19 频道密钥分发 / M20 表情回应、编辑、置顶）。
		if c.handleInboundControlText(env, text) {
			return
		}

		if pendingChannelCipher != "" {
			c.history.storePendingCipher(env.ID, pendingChannelCipher)
		}
		if c.history.append(appapi.ChatMessage{
			ID:       env.ID,
			From:     env.From,
			To:       env.To,
			Group:    env.Group,
			Type:     protocol.TextMsg,
			Text:     text,
			ReplyTo:  p.ReplyTo,
			Mentions: p.Mentions,
			TS:       env.TS,
		}) {
			c.recordInboundUnread(env.From, env.Group, text, p.Mentions)
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, protocol.TextMsg, text)
			if burnDur, ok := extractBurnDurationFromText(text); ok {
				c.scheduleBurnPurge(env.ID, env.From, env.Group, burnDur)
			}
		}

	case protocol.StickerMsg:
		var p protocol.StickerPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		text := p.Name
		if p.B64 != "" {
			mime := p.Mime
			if mime == "" {
				mime = "image/png"
			}
			text = "data:" + mime + ";base64," + p.B64
		}
		if c.history.append(appapi.ChatMessage{
			ID:    env.ID,
			From:  env.From,
			To:    env.To,
			Group: env.Group,
			Type:  protocol.StickerMsg,
			Text:  text,
			TS:    env.TS,
		}) {
			c.recordInboundUnread(env.From, env.Group, text, nil)
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, protocol.StickerMsg, text)
		}

	case protocol.MsgRevoke:
		var p protocol.MsgRevokePayload
		if err := env.DecodePayload(&p); err != nil || p.MsgID == "" {
			return
		}
		if updated, ok := c.history.revoke(p.MsgID, env.From, revokeWindow); ok {
			c.listener().OnMessageReceived(env.From, env.Group, p.MsgID, protocol.MsgRevoke, "[消息已撤回]")
			c.emitMessageUpdated(updated)
		}

	case protocol.MsgRead:
		var p protocol.MsgReadPayload
		if err := env.DecodePayload(&p); err != nil || p.MsgID == "" {
			return
		}
		if updated, ok := c.history.markRead(p.MsgID, env.From); ok {
			c.listener().OnMessageReceived(env.From, env.Group, p.MsgID, protocol.MsgRead, p.MsgID)
			c.emitMessageUpdated(updated)
		}

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
	case protocol.GroupKey:
		c.deliverGroupKey(env)
	case protocol.RelayGrant:
		// 群文件 grant 带 PeerID，优先投递群任务；未消费再按 1:1 任务路由。
		if !c.deliverGroupRelayGrant(env) {
			c.routeToTransfer(env)
		}

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

// handleInboundControlText 解析并执行加密通道内的控制指令（M19 频道密钥 / M20 消息互动）。
func (c *Client) handleInboundControlText(env *protocol.Envelope, text string) bool {
	switch {
	case strings.HasPrefix(text, "sys:chkey:"):
		rest := strings.TrimPrefix(text, "sys:chkey:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			chID, chKey := parts[0], parts[1]
			c.setChannelKey(chID, chKey)
			for _, m := range c.history.decryptChannelPending(chID, chKey) {
				if c.handleInboundControlText(&protocol.Envelope{
					ID:    m.ID,
					From:  m.From,
					To:    m.To,
					Group: m.Group,
					TS:    m.TS,
				}, m.Text) {
					continue
				}
				c.listener().OnMessageReceived(m.From, m.Group, m.ID, "MSG_EDIT", m.Text)
			}
		}
		return true

	case strings.HasPrefix(text, "sys:react:"):
		rest := strings.TrimPrefix(text, "sys:react:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			if updated, ok := c.history.reactBy(parts[0], env.From, parts[1]); ok {
				c.listener().OnMessageReceived(env.From, env.Group, parts[0], "MSG_REACT", updated.Text)
				c.emitMessageUpdated(updated)
			}
		}
		return true

	case strings.HasPrefix(text, "sys:edit:"):
		rest := strings.TrimPrefix(text, "sys:edit:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			if updated, ok := c.history.edit(parts[0], env.From, parts[1]); ok {
				c.listener().OnMessageReceived(env.From, env.Group, parts[0], "MSG_EDIT", updated.Text)
				c.emitMessageUpdated(updated)
			}
		}
		return true

	case strings.HasPrefix(text, "sys:pin:"):
		targetID := strings.TrimSpace(strings.TrimPrefix(text, "sys:pin:"))
		if targetID != "" {
			if updated, ok := c.history.pin(targetID); ok {
				c.listener().OnMessageReceived(env.From, env.Group, targetID, "MSG_PIN", updated.Text)
				c.emitMessageUpdated(updated)
			}
		}
		return true

	case strings.HasPrefix(text, "sys:clip:"):
		clipBody := strings.TrimPrefix(text, "sys:clip:")
		if clipBody != "" {
			c.mu.Lock()
			c.sharedClip = clipBody
			c.lastClipHash = hashClipboardText(clipBody)
			c.mu.Unlock()
			_ = writeSystemClipboardText(clipBody)
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "CLIP_SYNC", "📋 [剪贴板已同步] "+clipBody)
		}
		return true

	case strings.HasPrefix(text, "sys:burn:"):
		targetID := strings.TrimSpace(strings.TrimPrefix(text, "sys:burn:"))
		if targetID != "" {
			if c.history.purgeByID(targetID) {
				c.listener().OnMessageReceived(env.From, env.Group, targetID, "MSG_BURN", "🔥 [阅后即焚已销毁]")
			}
		}
		return true

	case strings.HasPrefix(text, "sys:call:"):
		rest := strings.TrimPrefix(text, "sys:call:")
		switch {
		case strings.HasPrefix(rest, "offer:"):
			remoteUDP := strings.TrimSpace(strings.TrimPrefix(rest, "offer:"))
			if remoteUDP != "" {
				c.onIncomingVoiceCallOffer(env.From, env.Group, remoteUDP)
				c.listener().OnMessageReceived(env.From, env.Group, env.ID, "VOICE_CALL_OFFER", "📞 [语音来电响铃] "+remoteUDP)
			}
		case strings.HasPrefix(rest, "accept:"):
			remoteUDP := strings.TrimSpace(strings.TrimPrefix(rest, "accept:"))
			if remoteUDP != "" {
				c.setVoiceRemoteUDP(remoteUDP)
				c.emitVoiceCallState()
				c.listener().OnMessageReceived(env.From, env.Group, env.ID, "VOICE_CALL", "📞 [对方已接听实时语音] "+remoteUDP)
			}
		case rest == "hangup":
			c.closeVoiceSession()
			c.emitVoiceCallState()
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "VOICE_CALL", "📞 [实时语音通话已结束]")
		case strings.HasPrefix(rest, "pcm:"):
			c.onInboundSignalingVoiceFrame(env.From, env.Group, strings.TrimPrefix(rest, "pcm:"))
		}
		return true

	case strings.HasPrefix(text, "sys:screen:"):
		rest := strings.TrimPrefix(text, "sys:screen:")
		switch {
		case strings.HasPrefix(rest, "start:"):
			fps := strings.TrimSpace(strings.TrimPrefix(rest, "start:"))
			fpsNum, _ := strconv.Atoi(fps)
			if fpsNum <= 0 {
				fpsNum = 15
			}
			c.mu.Lock()
			c.screenSharing = true
			c.screenShareFPS = fpsNum
			c.screenSharePeer = env.From
			c.screenShareGroup = env.Group
			c.mu.Unlock()
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "SCREEN_SHARE", "🖥️ [屏幕共享已开启 "+fps+"fps]")
		case strings.HasPrefix(rest, "frame:"):
			frame := strings.TrimSpace(strings.TrimPrefix(rest, "frame:"))
			c.recordScreenFrame(env.From, env.Group, 0, frame)
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "SCREEN_FRAME", frame)
			c.emitScreenShareFrame(env.From, env.Group, 0, frame)
		case strings.HasPrefix(rest, "annot:"):
			annot := strings.TrimSpace(strings.TrimPrefix(rest, "annot:"))
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "SCREEN_ANNOT", "📍 [屏幕批注 "+annot+"]")
		case rest == "stop":
			c.mu.Lock()
			c.screenSharing = false
			c.latestScreenFrame = ""
			c.mu.Unlock()
			c.emitScreenShareFrame(env.From, env.Group, 0, "")
			c.listener().OnMessageReceived(env.From, env.Group, env.ID, "SCREEN_SHARE", "🖥️ [屏幕共享已结束]")
		}
		return true

	case strings.HasPrefix(text, "sys:gossip:"):
		raw := strings.TrimSpace(strings.TrimPrefix(text, "sys:gossip:"))
		if raw != "" {
			parts := strings.Split(raw, ",")
			c.AddGossipEndpoints(parts...)
		}
		return true

	case strings.HasPrefix(text, "sys:sync:"):
		rest := strings.TrimPrefix(text, "sys:sync:")
		switch {
		case strings.HasPrefix(rest, "req:"):
			sinceStr := strings.TrimSpace(strings.TrimPrefix(rest, "req:"))
			sinceTs, _ := strconv.ParseInt(sinceStr, 10, 64)
			msgs := c.history.exportSince(sinceTs)
			if len(msgs) > 0 {
				if raw, err := json.Marshal(msgs); err == nil {
					b64 := base64.StdEncoding.EncodeToString(raw)
					_ = c.sendControlText(env.From, env.Group, "sys:sync:push:"+b64)
				}
			}
		case strings.HasPrefix(rest, "push:"):
			b64 := strings.TrimSpace(strings.TrimPrefix(rest, "push:"))
			if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
				var incoming []appapi.ChatMessage
				if json.Unmarshal(raw, &incoming) == nil && len(incoming) > 0 {
					merged := c.history.mergeRemoteMessages(incoming)
					if merged > 0 {
						c.listener().OnMessageReceived(env.From, env.Group, env.ID, "HISTORY_SYNC", fmt.Sprintf("🔄 [多端漫游已同步 %d 条历史消息]", merged))
					}
				}
			}
		}
		return true
	}
	return false
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

// recordInboundUnread 统计入站消息的未读计数与 @ 提及计数（M32）。
func (c *Client) recordInboundUnread(from, group, text string, mentions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if from == "" || from == c.selfID {
		return
	}
	key := sessionUnreadKey(from, group)
	c.unreadCounts[key]++

	mentioned := false
	lowerText := strings.ToLower(text)
	lowerNick := strings.ToLower(strings.TrimSpace(c.nickname))
	if lowerNick != "" && strings.Contains(lowerText, "@"+lowerNick) {
		mentioned = true
	} else if strings.Contains(lowerText, "@all") || strings.Contains(text, "@全体成员") || strings.Contains(text, "@所有人") {
		mentioned = true
	} else {
		for _, m := range mentions {
			if m == c.selfID || (lowerNick != "" && strings.EqualFold(m, lowerNick)) || strings.EqualFold(m, "all") {
				mentioned = true
				break
			}
		}
	}
	if mentioned {
		c.mentionCounts[key]++
	}
	sender := from
	if p, ok := c.peers[from]; ok && strings.TrimSpace(p.Nickname) != "" {
		sender = p.Nickname
	}
	dnd := c.dnd
	if !dnd {
		title := "💬 新消息 · " + sender
		if mentioned {
			title = "🔔 [有人@我] " + sender
		}
		preview := text
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		go c.notifyDesktopEvent(title, preview, mentioned)
	}
}
