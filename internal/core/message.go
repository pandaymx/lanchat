package core

import (
	"strings"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// SendText 发送文本。group 为空表示单播，"*" 为 G1 全体，其余为 G2 频道。
func (c *Client) SendText(to, text, group string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errEmptyText
	}
	if group == "" && to == "" {
		return "", errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	replyTo, mentions := extractMentionsAndReply(text)
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
