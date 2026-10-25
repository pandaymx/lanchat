package core

import (
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// revokeWindow 是发送者可撤回消息的最大时间窗口（2 分钟）。
const revokeWindow = 2 * time.Minute

// SendText 发送文本。group 为空表示单播，"*" 为 G1 全体，其余为 G2 频道。
// 支持 `/revoke <msgID>` 与 `/read <msgID>` 快捷指令触发消息撤回与已读回执。
func (c *Client) SendText(to, text, group string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", errEmptyText
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
			text = "「" + orig.Text + "」\n" + body
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
	payload := protocol.TextPayload{
		Text:     text,
		ReplyTo:  replyTo,
		Mentions: mentions,
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

	var (
		env   *protocol.Envelope
		group string
	)
	if isChannel || to == protocol.GroupBroadcast {
		group = to
		env, err = c.sendGroup(conn, protocol.StickerMsg, group, protocol.StickerPayload{Name: path})
	} else {
		env, err = c.sendTo(conn, protocol.StickerMsg, to, protocol.StickerPayload{Name: path})
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
		Text:  path,
		TS:    env.TS,
	})
	return env.ID, nil
}

func (c *Client) selfIDValue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selfID
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
