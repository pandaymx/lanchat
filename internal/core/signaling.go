package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// session 是一次信令连接（含其自动重连生命周期）。
type session struct {
	conn      *websocket.Conn
	heartbeat time.Duration
}

// Connect 拨号并完成 HELLO/WELCOME 握手；成功后常驻收泵，断线自动重连。
func (c *Client) Connect(addr, psk string) error {
	if strings.TrimSpace(addr) == "" {
		return errEmptyAddress
	}

	c.mu.Lock()
	if c.connState == appapi.ConnConnecting || c.connState == appapi.ConnConnected {
		c.mu.Unlock()
		return errInvalidState
	}
	c.addr = normalizeAddr(addr)
	c.psk = psk
	c.connState = appapi.ConnConnecting
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnecting, "")

	s, err := c.establish(c.runCtx)
	if err != nil {
		var reason string
		state := appapi.ConnDisconnected
		if errors.Is(err, errAuthFailed) {
			state = appapi.ConnAuthFailed
			reason = err.Error()
		} else {
			reason = err.Error()
		}
		c.mu.Lock()
		c.connState = state
		c.conn = nil
		c.mu.Unlock()
		c.listener().OnConnChanged(state, reason)
		return err
	}

	c.mu.Lock()
	c.connState = appapi.ConnConnected
	c.conn = s.conn
	c.gen++
	gen := c.gen
	c.heartbeat = int(s.heartbeat / time.Second)
	c.mu.Unlock()

	c.listener().OnConnChanged(appapi.ConnConnected, "")
	go c.watch(gen, s, time.Second)
	return nil
}

// establish 拨号 + HELLO/WELCOME + 同步消化首帧 USER_LIST，返回会话。
func (c *Client) establish(ctx context.Context) (*session, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, _, err := websocket.Dial(dialCtx, c.addr, nil)
	cancel()
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(int64(protocol.MaxControlPayload))

	c.mu.Lock()
	nick, deviceID, osName := c.nickname, c.deviceID, c.os
	psk := c.psk
	c.mu.Unlock()

	hello, err := buildEnvelope(protocol.Hello, protocol.HelloPayload{
		Nickname: nick,
		DeviceID: deviceID,
		PSK:      psk,
		OS:       osName,
		Caps: protocol.ClientCaps{
			Resume: true,
			Relay:  true,
		},
	})
	if err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	if err := c.writeJSON(conn, hello); err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}

	hCtx, hCancel := context.WithTimeout(ctx, handshakeTimeout)
	env, err := readEnvelope(hCtx, conn)
	hCancel()
	if err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	switch env.Type {
	case protocol.AuthFail:
		var p protocol.AuthFailPayload
		_ = env.DecodePayload(&p)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, fmtErrAuth(p.Reason)
	case protocol.Welcome:
	default:
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, errors.New("core: unexpected handshake frame " + env.Type)
	}

	var wp protocol.WelcomePayload
	if err := env.DecodePayload(&wp); err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	c.mu.Lock()
	c.selfID = wp.SelfID
	c.server = wp.AuthMode
	c.mu.Unlock()

	// USER_LIST 是握手后首帧，同步消化，避免 connected 事件早于在线表。
	listCtx, lCancel := context.WithTimeout(ctx, handshakeTimeout)
	listEnv, err := readEnvelope(listCtx, conn)
	lCancel()
	if err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, err
	}
	if listEnv.Type != protocol.UserList {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return nil, errors.New("core: expected USER_LIST, got " + listEnv.Type)
	}
	c.applyUserList(listEnv)

	hb := time.Duration(wp.HeartbeatInterval) * time.Second
	if hb <= 0 {
		hb = time.Duration(protocol.HeartbeatIntervalSec) * time.Second
	}
	return &session{conn: conn, heartbeat: hb}, nil
}

// watch 运行收泵与心跳；连接断开后按指数退避重连，直到 Close 或鉴权失败。
func (c *Client) watch(gen uint64, s *session, backoff time.Duration) {
	pumpErr := make(chan error, 1)
	go func() { pumpErr <- c.readPump(gen, s.conn) }()

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		c.heartbeatLoop(gen, s.conn, s.heartbeat)
	}()

	err := <-pumpErr
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
	<-heartbeatDone

	c.mu.Lock()
	curGen := c.gen
	c.conn = nil
	c.mu.Unlock()

	// Close 触发：停止重连。
	if c.runCtx.Err() != nil || curGen != gen {
		return
	}

	reason := ""
	if err != nil {
		reason = err.Error()
	}
	c.mu.Lock()
	c.connState = appapi.ConnConnecting
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnecting, reason)

	select {
	case <-c.runCtx.Done():
		return
	case <-time.After(backoff):
	}

	ns, estErr := c.establish(c.runCtx)
	if estErr != nil {
		st := appapi.ConnDisconnected
		if errors.Is(estErr, errAuthFailed) {
			st = appapi.ConnAuthFailed
		}
		c.mu.Lock()
		c.connState = st
		c.mu.Unlock()
		c.listener().OnConnChanged(st, estErr.Error())
		return
	}

	c.mu.Lock()
	c.connState = appapi.ConnConnected
	c.conn = ns.conn
	c.gen++
	newGen := c.gen
	c.heartbeat = int(ns.heartbeat / time.Second)
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnected, "")

	next := backoff * 2
	if next > maxBackoff {
		next = maxBackoff
	}
	c.watch(newGen, ns, next)
}

// heartbeatLoop 周期发送 HEARTBEAT；发送失败静默退出（收泵会感知断连）。
func (c *Client) heartbeatLoop(gen uint64, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	_ = c.sendHeartbeat(conn)
	for {
		select {
		case <-c.runCtx.Done():
			return
		case <-t.C:
			c.mu.Lock()
			curGen := c.gen
			curConn := c.conn
			c.mu.Unlock()
			if curGen != gen || curConn != conn {
				return
			}
			if err := c.sendHeartbeat(conn); err != nil {
				return
			}
		}
	}
}

func (c *Client) sendHeartbeat(conn *websocket.Conn) error {
	env, err := buildEnvelope(protocol.Heartbeat, nil)
	if err != nil {
		return err
	}
	return c.writeJSON(conn, env)
}

// readEnvelope 读取并解码一条信封。
func readEnvelope(ctx context.Context, conn *websocket.Conn) (*protocol.Envelope, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("core: expected text frame")
	}
	return protocol.UnmarshalEnvelope(data)
}

// normalizeAddr 补全 ws:// 前缀。
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "ws://") || strings.HasPrefix(addr, "wss://") {
		return addr
	}
	return "ws://" + addr
}

// 防止 uuid 未使用告警（部分文件不直接引用时保持 import 一致）。
var _ = uuid.NewString
