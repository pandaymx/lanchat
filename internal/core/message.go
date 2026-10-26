package core

import (
	"fmt"
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
		c.mu.Lock()
		c.e2eeKey = ""
		c.mu.Unlock()
		return "e2ee:off", nil
	}
	if strings.HasPrefix(trimmed, "/e2ee ") {
		secret := strings.TrimSpace(strings.TrimPrefix(trimmed, "/e2ee "))
		if secret == "" {
			return "", errEmptyText
		}
		c.mu.Lock()
		c.e2eeKey = secret
		c.mu.Unlock()
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
		if err := c.MarkRead(to, group, targetID); err != nil {
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
		if targetID == "" {
			return "", errEmptyText
		}
		updated, ok := c.history.react(targetID, emoji)
		if !ok {
			return "", errInvalidState
		}
		if err := c.sendControlText(to, group, "sys:react:"+targetID+":"+emoji); err != nil {
			return "", err
		}
		c.listener().OnMessageReceived(c.selfIDValue(), group, targetID, "MSG_REACT", updated.Text)
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
		updated, ok := c.history.edit(targetID, c.selfIDValue(), newBody)
		if !ok {
			return "", errInvalidState
		}
		if err := c.sendControlText(to, group, "sys:edit:"+targetID+":"+newBody); err != nil {
			return "", err
		}
		c.listener().OnMessageReceived(c.selfIDValue(), group, targetID, "MSG_EDIT", updated.Text)
		return targetID, nil
	}
	if strings.HasPrefix(trimmed, "/pin ") {
		targetID := strings.TrimSpace(strings.TrimPrefix(trimmed, "/pin "))
		if targetID == "" {
			return "", errEmptyText
		}
		updated, ok := c.history.pin(targetID)
		if !ok {
			return "", errInvalidState
		}
		if err := c.sendControlText(to, group, "sys:pin:"+targetID); err != nil {
			return "", err
		}
		c.listener().OnMessageReceived(c.selfIDValue(), group, targetID, "MSG_PIN", updated.Text)
		return targetID, nil
	}
	if trimmed == "/voice" || strings.HasPrefix(trimmed, "/voice ") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/voice"))
		text = formatVoiceMessage(rest)
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
	replyTo, mentions := extractMentionsAndReply(text)
	if explicitReplyTo != "" {
		replyTo = explicitReplyTo
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
	if _, ok := c.history.revoke(msgID, c.selfIDValue(), revokeWindow); !ok {
		return errInvalidState
	}
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
	c.history.markRead(msgID, c.selfIDValue())
	payload := protocol.MsgReadPayload{MsgID: msgID}
	if group != "" {
		_, err = c.sendGroup(conn, protocol.MsgRead, group, payload)
	} else {
		_, err = c.sendTo(conn, protocol.MsgRead, to, payload)
	}
	return err
}

// SearchHistory 在本地历史消息中按关键字（与可选会话 ID）检索未撤回消息。
func (c *Client) SearchHistory(query, convID string) []appapi.ChatMessage {
	return c.history.search(query, convID)
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

	if e2eeSecret != "" {
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
		return `🎤 [语音消息 · ` + filepath.Base(first) + `]`
	}
	return `🎤 [语音消息 3"] ` + raw
}
