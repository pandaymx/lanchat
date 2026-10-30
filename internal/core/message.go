package core

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// revokeWindow 是发送者可撤回消息的最大时间窗口（2 分钟）。
const revokeWindow = 2 * time.Minute

// SendText 发送文本。group 为空表示单播，"*" 为 G1 全体，其余为 G2 频道。
// 支持 `/revoke <msgID>`、`/read <msgID>`、`/reply <msgID> <text>`、`/e2ee <secret|off>`、
// `/fingerprint`（M15 TOFU 安全码核验）与 `/voice <duration|note>`（M17 语音消息）快捷指令。
func (c *Client) SendText(to, text, group string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", errEmptyText
	}
	if trimmed == "/status" || strings.HasPrefix(trimmed, "/status ") {
		diag := c.diagnosticSummary()
		return "status:" + diag, nil
	}
	if trimmed == "/mesh" || strings.HasPrefix(trimmed, "/mesh ") || trimmed == "/leader" || strings.HasPrefix(trimmed, "/leader ") {
		converged, leaderID := c.ConvergeAutoLeader()
		c.mu.Lock()
		addr := c.addr
		embedded := c.embeddedStop != nil
		peerCount := len(c.peers)
		c.mu.Unlock()
		if leaderID == "" {
			leaderID = "manual"
		}
		summary := fmt.Sprintf("mesh:leader=%s addr=%s embedded=%t converged=%t peers=%d", leaderID, addr, embedded, converged, peerCount)
		return summary, nil
	}
	if trimmed == "/update" || strings.HasPrefix(trimmed, "/update ") {
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/update"))
		return c.handleUpdateCommand(arg, to, group)
	}
	if trimmed == "/fingerprint" || strings.HasPrefix(trimmed, "/fingerprint ") {
		peerID := to
		if arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/fingerprint")); arg != "" {
			peerID = arg
		}
		selfFP := c.Fingerprint()
		peerFP := c.PeerFingerprint(peerID)
		ret := "fp:" + selfFP
		if peerFP != "" {
			ret += "/" + peerFP
		}
		return ret, nil
	}
	if trimmed == "/e2ee off" || trimmed == "/e2ee clear" {
		_ = c.SetSessionE2EE("")
		return "e2ee:off", nil
	}
	if strings.HasPrefix(trimmed, "/e2ee ") {
		secret := strings.TrimSpace(strings.TrimPrefix(trimmed, "/e2ee "))
		if secret == "" {
			return "", errEmptyText
		}
		if err := c.SetSessionE2EE(secret); err != nil {
			return "", err
		}
		return "e2ee:on", nil
	}
	if trimmed == "/bwlimit off" || trimmed == "/bwlimit 0" {
		transfer.SetBandwidthLimitBps(0)
		return "bwlimit:off", nil
	}
	if strings.HasPrefix(trimmed, "/bwlimit ") {
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "/bwlimit "))
		mb, err := strconv.ParseFloat(arg, 64)
		if err != nil || mb < 0 {
			return "", errEmptyText
		}
		if mb == 0 {
			transfer.SetBandwidthLimitBps(0)
			return "bwlimit:off", nil
		}
		transfer.SetBandwidthLimitBps(int64(mb * 1024 * 1024))
		return fmt.Sprintf("bwlimit:%.1fMB/s", mb), nil
	}
	if strings.HasPrefix(trimmed, "/senddir ") || strings.HasPrefix(trimmed, "/folder ") {
		dirPath := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "/senddir"), "/folder"))
		if dirPath == "" {
			return "", errEmptyPath
		}
		if group != "" {
			return c.OfferFileToGroup(group, dirPath)
		}
		return c.OfferFile(to, dirPath)
	}
	if strings.HasPrefix(trimmed, "/paste ") {
		pasteTarget := strings.TrimSpace(strings.TrimPrefix(trimmed, "/paste "))
		if pasteTarget == "" {
			return "", errEmptyPath
		}
		if group != "" {
			return c.OfferFileToGroup(group, pasteTarget)
		}
		return c.OfferFile(to, pasteTarget)
	}
	if trimmed == "/clip" || trimmed == "/clip get" {
		return "clip:" + c.LastClipboard(), nil
	}
	if strings.HasPrefix(trimmed, "/clip ") {
		clipText := strings.TrimSpace(strings.TrimPrefix(trimmed, "/clip "))
		clipText = strings.TrimSpace(strings.TrimPrefix(clipText, "push "))
		if clipText == "" {
			return "", errEmptyText
		}
		if group == "" && to == "" {
			return "", errPeerNotFound
		}
		c.mu.Lock()
		c.sharedClip = clipText
		c.lastClipHash = hashClipboardText(clipText)
		c.mu.Unlock()
		if err := c.sendControlText(to, group, "sys:clip:"+clipText); err != nil {
			return "", err
		}
		return "clip:" + clipText, nil
	}
	if trimmed == "/dnd" || strings.HasPrefix(trimmed, "/dnd ") {
		arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "/dnd")))
		c.mu.Lock()
		switch arg {
		case "on", "true", "1":
			c.dnd = true
		case "off", "false", "0":
			c.dnd = false
		case "status":
		default:
			c.dnd = !c.dnd
		}
		dnd := c.dnd
		c.mu.Unlock()
		if dnd {
			return "dnd:on", nil
		}
		return "dnd:off", nil
	}
	if trimmed == "/unread" || strings.HasPrefix(trimmed, "/unread ") {
		arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "/unread")))
		if arg == "clear" || arg == "reset" {
			c.ClearUnread("", "")
		}
		unread, mentions := c.UnreadTotal()
		return fmt.Sprintf("unread:%d mentions:%d dnd:%t", unread, mentions, c.IsDND()), nil
	}
	if trimmed == "/compact" {
		removed := c.CompactHistory()
		return fmt.Sprintf("status:compacted:%d", removed), nil
	}
	if trimmed == "/history" || strings.HasPrefix(trimmed, "/history ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/history"))
		fields := strings.Fields(rest)
		limit := 50
		var beforeTS int64
		if len(fields) >= 1 {
			if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
				limit = n
			}
		}
		if len(fields) >= 2 {
			if ts, err := strconv.ParseInt(fields[1], 10, 64); err == nil && ts > 0 {
				beforeTS = ts
			}
		}
		convID := group
		if convID == "" {
			convID = to
		}
		items := c.HistoryPage(convID, beforeTS, limit)
		return fmt.Sprintf("history:%d", len(items)), nil
	}
	if res, handled, err := c.handleCollabCommand(trimmed, to, group); handled {
		return res, err
	}
	if group == "" && to == "" {
		return "", errPeerNotFound
	}
	if strings.HasPrefix(trimmed, "/revoke ") {
		targetID := strings.TrimSpace(strings.TrimPrefix(trimmed, "/revoke "))
		if err := c.RevokeMessage(to, group, targetID); err != nil {
			return "", err
		}
		return targetID, nil
	}
	if strings.HasPrefix(trimmed, "/read ") {
		targetID := strings.TrimSpace(strings.TrimPrefix(trimmed, "/read "))
		if err := c.MarkMessageRead(to, group, targetID); err != nil {
			return "", err
		}
		return targetID, nil
	}
	if trimmed == "/react" || strings.HasPrefix(trimmed, "/react ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/react"))
		parts := strings.SplitN(rest, " ", 2)
		targetID := ""
		if len(parts) > 0 {
			targetID = strings.TrimSpace(parts[0])
		}
		emoji := "👍"
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			emoji = strings.TrimSpace(parts[1])
		} else if len(parts) == 1 && isReactionEmojiToken(targetID) {
			emoji = targetID
			targetID = ""
		}
		if targetID == "" {
			targetID = c.history.lastMessageID(to, group)
		}
		if err := c.ReactMessage(to, group, targetID, emoji); err != nil {
			return "", err
		}
		return targetID, nil
	}
	if strings.HasPrefix(trimmed, "/edit ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/edit "))
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", errEmptyText
		}
		targetID := strings.TrimSpace(parts[0])
		newBody := strings.TrimSpace(parts[1])
		if err := c.EditMessage(to, group, targetID, newBody); err != nil {
			return "", err
		}
		return targetID, nil
	}
	if strings.HasPrefix(trimmed, "/pin ") {
		targetID := strings.TrimSpace(strings.TrimPrefix(trimmed, "/pin "))
		if err := c.PinMessage(to, group, targetID); err != nil {
			return "", err
		}
		return targetID, nil
	}
	if trimmed == "/voice" || strings.HasPrefix(trimmed, "/voice ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/voice"))
		text = formatVoiceMessage(rest)
	}
	var burnDur time.Duration
	if strings.HasPrefix(trimmed, "/burn ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/burn "))
		dur, body := parseBurnCommand(rest)
		if body == "" {
			return "", errEmptyText
		}
		burnDur = dur
		text = fmt.Sprintf("🔥 [阅后即焚 %s] %s", formatBurnDuration(dur), body)
	}
	var explicitReplyTo string
	if strings.HasPrefix(trimmed, "/reply ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/reply "))
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return "", errEmptyText
		}
		explicitReplyTo = strings.TrimSpace(parts[0])
		body := strings.TrimSpace(parts[1])
		if orig, ok := c.history.findByID(explicitReplyTo); ok && !orig.Revoked {
			cleanOrig, _ := splitReactionBadges(orig.Text)
			text = "「" + cleanOrig + "」\n" + body
		} else {
			text = "[re:" + explicitReplyTo + "] " + body
		}
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	if group != "" && group != protocol.GroupBroadcast && c.isChannelMemberMuted(group, c.selfIDValue()) {
		return "", errChannelMuted
	}
	replyTo, mentions := extractMentionsAndReply(text)
	if explicitReplyTo != "" {
		replyTo = explicitReplyTo
	}
	if group == "" && burnDur == 0 && !strings.HasPrefix(text, "sys:") {
		c.mu.Lock()
		_, peerOnline := c.peers[to]
		peerPub := c.peerPubs[to]
		e2eeSecret := c.e2eeKey
		c.mu.Unlock()
		if !peerOnline && peerPub != "" && e2eeSecret == "" {
			return c.enqueueOfflineTextWithMeta(to, text, peerPub, replyTo, mentions)
		}
	}
	wireText, senderPub, err := c.sealOutboundText(to, group, text)
	if err != nil {
		return "", err
	}
	payload := protocol.TextPayload{
		Text:      wireText,
		ReplyTo:   replyTo,
		Mentions:  mentions,
		SenderPub: senderPub,
	}
	var env *protocol.Envelope
	if group != "" {
		env, err = c.sendGroup(conn, protocol.TextMsg, group, payload)
	} else {
		env, err = c.sendTo(conn, protocol.TextMsg, to, payload)
	}
	if err != nil {
		return "", err
	}
	c.history.append(appapi.ChatMessage{
		ID:       env.ID,
		From:     c.selfIDValue(),
		To:       to,
		Group:    group,
		Type:     protocol.TextMsg,
		Text:     text,
		ReplyTo:  replyTo,
		Mentions: mentions,
		TS:       env.TS,
	})
	if burnDur > 0 {
		c.scheduleBurnPurge(env.ID, to, group, burnDur)
	}
	return env.ID, nil
}

// RevokeMessage 撤回本机在 2 分钟内发送的消息并同步给对端或群组。
func (c *Client) RevokeMessage(to, group, msgID string) error {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return errEmptyText
	}
	if group == "" && to == "" {
		return errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	updated, ok := c.history.revoke(msgID, c.selfIDValue(), revokeWindow)
	if !ok {
		return errInvalidState
	}
	c.emitMessageUpdated(updated)
	payload := protocol.MsgRevokePayload{MsgID: msgID}
	if group != "" {
		_, err = c.sendGroup(conn, protocol.MsgRevoke, group, payload)
	} else {
		_, err = c.sendTo(conn, protocol.MsgRevoke, to, payload)
	}
	return err
}

// MarkRead 发送消息已读回执并更新本地历史记录。
func (c *Client) MarkRead(to, group, msgID string) error {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return errEmptyText
	}
	if group == "" && to == "" {
		return errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	if updated, ok := c.history.markRead(msgID, c.selfIDValue()); ok {
		c.emitMessageUpdated(updated)
	}
	payload := protocol.MsgReadPayload{MsgID: msgID}
	if group != "" {
		_, err = c.sendGroup(conn, protocol.MsgRead, group, payload)
	} else {
		_, err = c.sendTo(conn, protocol.MsgRead, to, payload)
	}
	return err
}

// MarkMessageRead 发送消息已读回执、更新本地历史并清零对应会话未读计数（契约 v1.4）。
func (c *Client) MarkMessageRead(to, group, msgID string) error {
	if err := c.MarkRead(to, group, msgID); err != nil {
		return err
	}
	c.ClearUnread(to, group)
	return nil
}

// ReactMessage 为指定消息添加表情回应并同步给对端或群组（契约 v1.4）。
func (c *Client) ReactMessage(to, group, msgID, emoji string) error {
	msgID = strings.TrimSpace(msgID)
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		emoji = "👍"
	}
	if msgID == "" {
		msgID = c.history.lastMessageID(to, group)
	}
	if msgID == "" {
		return errEmptyText
	}
	if group == "" && to == "" {
		return errPeerNotFound
	}
	updated, ok := c.history.reactBy(msgID, c.selfIDValue(), emoji)
	if !ok {
		return errInvalidState
	}
	if err := c.sendControlText(to, group, "sys:react:"+msgID+":"+emoji); err != nil {
		return err
	}
	c.listener().OnMessageReceived(c.selfIDValue(), group, msgID, "MSG_REACT", updated.Text)
	c.emitMessageUpdated(updated)
	return nil
}

// EditMessage 原地二次编辑本机已发送的消息并同步给对端或群组（契约 v1.4）。
func (c *Client) EditMessage(to, group, msgID, newText string) error {
	msgID = strings.TrimSpace(msgID)
	newText = strings.TrimSpace(newText)
	if msgID == "" || newText == "" {
		return errEmptyText
	}
	if group == "" && to == "" {
		return errPeerNotFound
	}
	updated, ok := c.history.edit(msgID, c.selfIDValue(), newText)
	if !ok {
		return errInvalidState
	}
	if err := c.sendControlText(to, group, "sys:edit:"+msgID+":"+newText); err != nil {
		return err
	}
	c.listener().OnMessageReceived(c.selfIDValue(), group, msgID, "MSG_EDIT", updated.Text)
	c.emitMessageUpdated(updated)
	return nil
}

// PinMessage 将指定消息标记为会话置顶公告并同步给对端或群组（契约 v1.4）。
func (c *Client) PinMessage(to, group, msgID string) error {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return errEmptyText
	}
	if group == "" && to == "" {
		return errPeerNotFound
	}
	updated, ok := c.history.pin(msgID)
	if !ok {
		return errInvalidState
	}
	if err := c.sendControlText(to, group, "sys:pin:"+msgID); err != nil {
		return err
	}
	c.listener().OnMessageReceived(c.selfIDValue(), group, msgID, "MSG_PIN", updated.Text)
	c.emitMessageUpdated(updated)
	return nil
}

// SetSessionE2EE 设置或清除会话端到端对称加密密钥（secret 为空或 "off" 时关闭 E2EE，契约 v1.4）。
func (c *Client) SetSessionE2EE(secret string) error {
	s := strings.TrimSpace(secret)
	if strings.EqualFold(s, "off") || strings.EqualFold(s, "clear") {
		s = ""
	}
	c.mu.Lock()
	c.e2eeKey = s
	c.mu.Unlock()
	return nil
}

// GetHistoryPage 按会话 ID 与游标时间戳 beforeTS 分页拉取本地加密历史消息（契约 v1.4）。
func (c *Client) GetHistoryPage(convID string, beforeTS int64, limit int) []appapi.ChatMessage {
	return c.HistoryPage(convID, beforeTS, limit)
}

// VerifyPeerFingerprint 核验本机与指定对端的 X25519 TOFU 安全码指纹（契约 v1.5）。
func (c *Client) VerifyPeerFingerprint(peerID string) string {
	selfFP := c.Fingerprint()
	peerFP := c.PeerFingerprint(strings.TrimSpace(peerID))
	ret := "fp:" + selfFP
	if peerFP != "" {
		ret += "/" + peerFP
	}
	return ret
}

// SendBurnMessage 发送阅后即焚定时销毁消息（契约 v1.5）。
func (c *Client) SendBurnMessage(to, group, text string, ttlSec int) (string, error) {
	body := strings.TrimSpace(text)
	if body == "" {
		return "", errEmptyText
	}
	if ttlSec <= 0 {
		ttlSec = 10
	}
	return c.SendText(to, fmt.Sprintf("/burn %ds %s", ttlSec, body), group)
}

// SetBandwidthLimit 设置全局文件传输带宽限速（MB/s，<=0 表示不限速，契约 v1.5）。
func (c *Client) SetBandwidthLimit(limitMBps float64) string {
	if limitMBps <= 0 {
		transfer.SetBandwidthLimitBps(0)
		return "bwlimit:off"
	}
	transfer.SetBandwidthLimitBps(int64(limitMBps * 1024 * 1024))
	return fmt.Sprintf("bwlimit:%.1fMB/s", limitMBps)
}

// CheckSilentUpdate 通过国内加速代理链检查或静默下载最新安装包（契约 v1.5）。
func (c *Client) CheckSilentUpdate(proxy string, download bool) (string, error) {
	res, err := c.CheckAndDownloadUpdate(c.runCtx, !download, strings.TrimSpace(proxy))
	if err != nil {
		return "", err
	}
	return "update:" + res.Summary, nil
}

// SearchHistory 在本地历史消息中按关键字（与可选会话 ID）检索未撤回消息。
func (c *Client) SearchHistory(query, convID string) []appapi.ChatMessage {
	return c.history.search(query, convID)
}

// CompactHistory 手动触发本地 JSONL 历史文件 Compaction，合并同一 MsgID 的多次变更并原子重写（阶段三 v1.19）。
func (c *Client) CompactHistory() int {
	return c.history.compact()
}

// HistoryPage 按会话 ID 与游标时间戳 beforeTS 分页返回历史消息（阶段三 v1.19）。
func (c *Client) HistoryPage(convID string, beforeTS int64, limit int) []appapi.ChatMessage {
	return c.history.page(convID, beforeTS, limit)
}

func (c *Client) emitMessageUpdated(msg appapi.ChatMessage) {
	if ul, ok := c.listener().(appapi.MessageUpdateListener); ok {
		ul.OnMessageUpdated(msg)
	}
}

// SendSticker 发送表情。支持单播对端 ID 或已加入的频道 ID（贴纸以名称/路径引用）。
func (c *Client) SendSticker(to, path string) (string, error) {
	if to == "" {
		return "", errPeerNotFound
	}
	if path == "" {
		return "", errEmptyPath
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	_, isChannel := c.channels[to]
	c.mu.Unlock()

	payload := protocol.StickerPayload{Name: path}
	histText := path
	if info, errOpen := openFileInfo(path); errOpen == nil {
		if sp, dataURI, ok := buildInlineStickerPayload(info, ""); ok {
			payload = sp
			histText = dataURI
		}
		_ = info.file.Close()
	}

	var (
		env   *protocol.Envelope
		group string
	)
	if isChannel || to == protocol.GroupBroadcast {
		group = to
		env, err = c.sendGroup(conn, protocol.StickerMsg, group, payload)
	} else {
		env, err = c.sendTo(conn, protocol.StickerMsg, to, payload)
	}
	if err != nil {
		return "", err
	}
	c.history.append(appapi.ChatMessage{
		ID:    env.ID,
		From:  c.selfIDValue(),
		To:    to,
		Group: group,
		Type:  protocol.StickerMsg,
		Text:  histText,
		TS:    env.TS,
	})
	return env.ID, nil
}

// sealOutboundText 根据会话类型封装文本密文（手动 /e2ee、1:1 自动 X25519 ECDH 或 G2 频道对称密钥，M13/M15/M19）。
func (c *Client) sealOutboundText(to, group, plain string) (wireText, senderPub string, err error) {
	c.mu.Lock()
	e2eeSecret := c.e2eeKey
	idPriv := c.identityPriv
	idPub := c.identityPub
	var peerPub string
	if group == "" && to != "" {
		peerPub = c.peerPubs[to]
	}
	c.mu.Unlock()

	if e2eeSecret != "" && !strings.HasPrefix(plain, "sys:") {
		sealed, err := transfer.SealText(e2eeSecret, plain)
		if err != nil {
			return "", "", err
		}
		return sealed, "", nil
	}
	if group == "" && peerPub != "" && idPriv != nil {
		sealed, err := transfer.SealECDHText(idPriv, peerPub, plain)
		if err != nil {
			return "", "", err
		}
		return sealed, idPub, nil
	}
	if group != "" && group != protocol.GroupBroadcast {
		if chKey := c.ensureChannelKey(group); chKey != "" {
			sealed, err := transfer.SealChannelText(chKey, plain)
			if err != nil {
				return "", "", err
			}
			return sealed, "", nil
		}
	}
	return plain, "", nil
}

// sendControlText 发送消息互动控制信封（sys:react: / sys:edit: / sys:pin:，M20）。
func (c *Client) sendControlText(to, group, ctrlPlain string) error {
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	wireText, senderPub, err := c.sealOutboundText(to, group, ctrlPlain)
	if err != nil {
		return err
	}
	payload := protocol.TextPayload{
		Text:      wireText,
		SenderPub: senderPub,
	}
	if group != "" {
		_, err = c.sendGroup(conn, protocol.TextMsg, group, payload)
	} else {
		_, err = c.sendTo(conn, protocol.TextMsg, to, payload)
	}
	return err
}

func (c *Client) selfIDValue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selfID != "" {
		return c.selfID
	}
	return c.deviceID
}

// diagnosticSummary 汇总当前客户端连接、安全指纹、在线成员、频道、CAS 索引与限速状态（M25）。
func (c *Client) diagnosticSummary() string {
	c.mu.Lock()
	conn := string(c.connState)
	id := c.selfID
	if id == "" {
		id = c.deviceID
	}
	pub := c.identityPub
	peers := len(c.peers)
	channels := len(c.channels)
	casCount := len(c.casIndex)
	c.mu.Unlock()

	bw := transfer.BandwidthLimitBps()
	bwStr := "off"
	if bw > 0 {
		bwStr = fmt.Sprintf("%.1fMB/s", float64(bw)/(1024*1024))
	}
	return fmt.Sprintf("conn=%s id=%s fp=%s peers=%d channels=%d cas=%d bw=%s",
		conn, id, IdentityFingerprint(pub), peers, channels, casCount, bwStr)
}

// connectedConn 返回当前信令连接，未连接报错。
func (c *Client) connectedConn() (*websocket.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connState != appapi.ConnConnected || c.conn == nil {
		return nil, errNotConnected
	}
	return c.conn, nil
}

// formatVoiceMessage 将 /voice 指令参数格式化为标准语音气泡文本（M17）。
func formatVoiceMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return `🎤 [语音消息 3"]`
	}
	parts := strings.SplitN(raw, " ", 2)
	first := parts[0]
	allDigits := len(first) > 0
	for _, r := range strings.TrimSuffix(strings.TrimSuffix(first, "s"), `"`) {
		if !unicode.IsDigit(r) {
			allDigits = false
			break
		}
	}
	if allDigits && strings.TrimSuffix(strings.TrimSuffix(first, "s"), `"`) != "" {
		sec := strings.TrimSuffix(strings.TrimSuffix(first, "s"), `"`)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return `🎤 [语音消息 ` + sec + `"] ` + strings.TrimSpace(parts[1])
		}
		return `🎤 [语音消息 ` + sec + `"]`
	}
	ext := strings.ToLower(filepath.Ext(first))
	if ext == ".wav" || ext == ".mp3" || ext == ".ogg" || ext == ".m4a" || ext == ".opus" || ext == ".aac" {
		if rawBytes, err := os.ReadFile(first); err == nil && len(rawBytes) > 0 && len(rawBytes) <= 512*1024 {
			mime := "audio/mp4"
			switch ext {
			case ".wav":
				mime = "audio/wav"
			case ".mp3":
				mime = "audio/mpeg"
			case ".ogg", ".opus":
				mime = "audio/ogg"
			}
			sec := len(rawBytes) / 3000
			if sec < 1 {
				sec = 1
			}
			if sec > 60 {
				sec = 60
			}
			return fmt.Sprintf("🎤 [语音消息 %d\"] data:%s;base64,%s", sec, mime, base64.StdEncoding.EncodeToString(rawBytes))
		}
		return `🎤 [语音消息 · ` + filepath.Base(first) + `]`
	}
	return `🎤 [语音消息 3"] ` + raw
}

// parseBurnCommand 解析 /burn [时长] <正文> 指令，默认自毁倒计时为 10 秒（M30）。
func parseBurnCommand(raw string) (time.Duration, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, ""
	}
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) == 2 {
		first := strings.TrimSpace(parts[0])
		body := strings.TrimSpace(parts[1])
		if d, err := time.ParseDuration(first); err == nil && d > 0 {
			return clampBurnDuration(d), body
		}
		if sec, err := strconv.Atoi(strings.TrimSuffix(first, "s")); err == nil && sec > 0 {
			return clampBurnDuration(time.Duration(sec) * time.Second), body
		}
	}
	return 10 * time.Second, raw
}

func clampBurnDuration(d time.Duration) time.Duration {
	if d < 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	if d > 24*time.Hour {
		return 24 * time.Hour
	}
	return d
}

func formatBurnDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func extractBurnDurationFromText(text string) (time.Duration, bool) {
	const prefix = "🔥 [阅后即焚 "
	if !strings.HasPrefix(text, prefix) {
		return 0, false
	}
	rest := text[len(prefix):]
	closeIdx := strings.IndexByte(rest, ']')
	if closeIdx <= 0 {
		return 0, false
	}
	durToken := strings.TrimSpace(rest[:closeIdx])
	if d, err := time.ParseDuration(durToken); err == nil && d > 0 {
		return clampBurnDuration(d), true
	}
	return 10 * time.Second, true
}

// scheduleBurnPurge 启动阅后即焚倒计时，到期后从本机内存与加密历史彻底抹除并同步通知对端销毁（M30）。
func (c *Client) scheduleBurnPurge(msgID, to, group string, dur time.Duration) {
	if msgID == "" || dur <= 0 {
		return
	}
	go func() {
		timer := time.NewTimer(dur)
		defer timer.Stop()
		select {
		case <-c.runCtx.Done():
			return
		case <-timer.C:
			if c.history.purgeByID(msgID) {
				_ = c.sendControlText(to, group, "sys:burn:"+msgID)
				c.listener().OnMessageReceived(c.selfIDValue(), group, msgID, "MSG_BURN", "🔥 [阅后即焚已销毁]")
			}
		}
	}()
}
