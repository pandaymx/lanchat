package core

import (
	"context"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// writeJSON 串行化发送一条信封（coder/websocket 同时刻仅允许一个写者）。
func (c *Client) writeJSON(conn *websocket.Conn, env *protocol.Envelope) error {
	data, err := env.Marshal()
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(c.runCtx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

// buildEnvelope 构造信封（payload 可为 nil）。
func buildEnvelope(typ string, payload any) (*protocol.Envelope, error) {
	return protocol.NewEnvelope(uuid.NewString(), typ, payload)
}

// sendTo 构造并向 to 发送一条信封，返回已发送信封。
func (c *Client) sendTo(conn *websocket.Conn, typ, to string, payload any) (*protocol.Envelope, error) {
	env, err := buildEnvelope(typ, payload)
	if err != nil {
		return nil, err
	}
	env.To = to
	if err := c.writeJSON(conn, env); err != nil {
		return nil, err
	}
	return env, nil
}

// sendGroup 构造并发往群组（G1 全体或 G2 频道）的信封。
// 群组路由依据 Group 字段，服务器忽略 To，故留空。
func (c *Client) sendGroup(conn *websocket.Conn, typ, group string, payload any) (*protocol.Envelope, error) {
	env, err := buildEnvelope(typ, payload)
	if err != nil {
		return nil, err
	}
	env.Group = group
	if err := c.writeJSON(conn, env); err != nil {
		return nil, err
	}
	return env, nil
}

// sendPresenceUpdate 把当前昵称 / 状态经 PRESENCE_UPDATE 同步给服务器。
func (c *Client) sendPresenceUpdate(conn *websocket.Conn) error {
	c.mu.Lock()
	nick, selfID, osName := c.nickname, c.selfID, c.os
	c.mu.Unlock()
	env, err := buildEnvelope(protocol.PresenceUpdate, protocol.UserUpdatePayload{
		User: protocol.User{ID: selfID, Nickname: nick, OS: osName},
	})
	if err != nil {
		return err
	}
	return c.writeJSON(conn, env)
}
