package core

import (
	"strings"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// SendText 发送文本。M4 仅支持单播（group 必须为空）；全体 / 频道属 M9。
func (c *Client) SendText(to, text, group string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errEmptyText
	}
	if group != "" {
		return "", errNotImplemented
	}
	if to == "" {
		return "", errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	env, err := c.sendTo(conn, protocol.TextMsg, to, protocol.TextPayload{Text: text})
	if err != nil {
		return "", err
	}
	return env.ID, nil
}

// SendSticker 发送表情。M4 仅支持单播，贴纸以名称引用（UI 本地缓存）。
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
	env, err := c.sendTo(conn, protocol.StickerMsg, to, protocol.StickerPayload{Name: path})
	if err != nil {
		return "", err
	}
	return env.ID, nil
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
