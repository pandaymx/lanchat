package core

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

// session 是一次信令连接（含其自动重连生命周期）。
type session struct {
	conn      *websocket.Conn
	heartbeat time.Duration
}

// Connect 拨号并完成 HELLO/WELCOME 握手；成功后常驻收泵，断线自动重连。
// 当 addr 为空、"auto" 或 "local" 时（M13/M14 零配置 LAN 自动选主与故障转移模式）：
//   - "auto" 或 ""：先执行 BrowseServers() 嗅探局域网存活节点（按 ID 字典序最小选主），
//     若无存活节点则全网卡自举内嵌服务器并广播 mDNS；
//   - "local"：直接在本地启动内嵌服务器并广播 mDNS。
func (c *Client) Connect(addr, psk string) error {
	c.mu.Lock()
	if c.connState == appapi.ConnConnecting || c.connState == appapi.ConnConnected {
		c.mu.Unlock()
		return errInvalidState
	}
	trimmedAddr := strings.TrimSpace(addr)
	isAuto := trimmedAddr == "" || strings.EqualFold(trimmedAddr, "auto")
	isLocal := strings.EqualFold(trimmedAddr, "local")
	c.autoMode = isAuto || isLocal
	c.psk = psk
	if !isAuto && !isLocal {
		c.addr = normalizeAddr(trimmedAddr)
	}
	c.connState = appapi.ConnConnecting
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnecting, "")

	var (
		s   *session
		err error
	)
	if isAuto {
		s, err = c.establishAuto(c.runCtx, psk)
	} else if isLocal {
		var bootAddr string
		bootAddr, err = c.startEmbeddedServer(psk)
		if err == nil {
			c.mu.Lock()
			c.addr = normalizeAddr(bootAddr)
			c.mu.Unlock()
			s, err = c.establish(c.runCtx)
		}
	} else {
		s, err = c.establish(c.runCtx)
	}
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
	c.channelKeySent = map[string]map[string]bool{}
	hasDesiredChannels := len(c.desiredChannels) > 0
	c.mu.Unlock()

	c.listener().OnConnChanged(appapi.ConnConnected, "")
	go c.watch(gen, s, time.Second)
	if hasDesiredChannels {
		_, _ = c.sendTo(s.conn, protocol.ChannelList, "", nil)
	}
	return nil
}

// establishAuto 执行 M14 “先嗅探、后自举”：优先连入 BrowseServers() 中字典序最小的存活节点，
// 若无可用节点则拉起本机全网卡内嵌服务器并广播 mDNS。
func (c *Client) establishAuto(ctx context.Context, psk string) (*session, error) {
	servers := c.BrowseServers()
	for _, srv := range servers {
		if strings.TrimSpace(srv.Addr) == "" {
			continue
		}
		candAddr := normalizeAddr(srv.Addr)
		c.mu.Lock()
		c.addr = candAddr
		c.psk = psk
		c.mu.Unlock()
		if s, err := c.establish(ctx); err == nil {
			return s, nil
		}
	}

	bootAddr, err := c.startEmbeddedServer(psk)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.addr = normalizeAddr(bootAddr)
	c.psk = psk
	c.mu.Unlock()
	return c.establish(ctx)
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
	nick, deviceID, osName, pubKey := c.nickname, c.deviceID, c.os, c.identityPub
	psk := c.psk
	c.mu.Unlock()

	hello, err := buildEnvelope(protocol.Hello, protocol.HelloPayload{
		Nickname: nick,
		DeviceID: deviceID,
		PSK:      psk,
		PubKey:   pubKey,
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

	hbCtx, cancelHB := context.WithCancel(c.runCtx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		c.heartbeatLoop(hbCtx, gen, s.conn, s.heartbeat)
	}()

	err := <-pumpErr
	cancelHB()
	_ = s.conn.CloseNow()
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
	autoMode := c.autoMode
	psk := c.psk
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnecting, reason)

	waitDur := backoff
	if autoMode && waitDur > 120*time.Millisecond {
		waitDur = 120 * time.Millisecond
	}

	var (
		ns        *session
		estErr    error
		failCount int
	)
	for {
		select {
		case <-c.runCtx.Done():
			return
		case <-time.After(waitDur):
		}

		if autoMode && failCount >= 2 {
			ns, estErr = c.establishAuto(c.runCtx, psk)
		} else {
			ns, estErr = c.establish(c.runCtx)
		}
		if estErr == nil {
			break
		}
		failCount++
		if !autoMode {
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
		if failCount > 6 {
			c.mu.Lock()
			c.connState = appapi.ConnDisconnected
			c.mu.Unlock()
			c.listener().OnConnChanged(appapi.ConnDisconnected, estErr.Error())
			return
		}
	}

	c.mu.Lock()
	c.connState = appapi.ConnConnected
	c.conn = ns.conn
	c.gen++
	newGen := c.gen
	c.heartbeat = int(ns.heartbeat / time.Second)
	c.channelKeySent = map[string]map[string]bool{}
	hasDesiredChannels := len(c.desiredChannels) > 0
	c.mu.Unlock()
	c.listener().OnConnChanged(appapi.ConnConnected, "")
	if hasDesiredChannels {
		_, _ = c.sendTo(ns.conn, protocol.ChannelList, "", nil)
	}

	next := backoff * 2
	if next > maxBackoff {
		next = maxBackoff
	}
	c.watch(newGen, ns, next)
}

// heartbeatLoop 周期发送 HEARTBEAT；发送失败或连接断开时立即退出。
func (c *Client) heartbeatLoop(ctx context.Context, gen uint64, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	_ = c.sendHeartbeat(conn)
	for {
		select {
		case <-ctx.Done():
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

// normalizeAddr 补全 ws:// 前缀与默认路径。
func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if !strings.HasPrefix(addr, "ws://") && !strings.HasPrefix(addr, "wss://") {
		addr = "ws://" + addr
	}
	// 若只有 host:port 而无路径，默认补上 /lanchat。
	schemeEnd := strings.Index(addr, "://")
	if schemeEnd > 0 {
		rest := addr[schemeEnd+3:]
		if !strings.Contains(rest, "/") {
			addr += "/lanchat"
		}
	}
	return addr
}

// startEmbeddedServer 在本机全网卡随机端口启动内嵌信令与中继服务器，
// 自动启动 mDNS 广播并随 c.runCtx 生命周期自动关停（M13/M14）。
func (c *Client) startEmbeddedServer(psk string) (string, error) {
	c.mu.Lock()
	if c.embeddedStop != nil {
		c.embeddedStop()
		c.embeddedStop = nil
	}
	nick := c.nickname
	devID := c.deviceID
	c.mu.Unlock()

	authMode := "none"
	var pskHash string
	if psk != "" {
		h, err := config.HashPSK(psk)
		if err != nil {
			return "", err
		}
		authMode = "psk"
		pskHash = h
	}

	relayHost := "127.0.0.1"
	if cands := discover.CandidateAddrs(0); len(cands) > 0 {
		if h, _, err := net.SplitHostPort(cands[0]); err == nil && h != "" {
			relayHost = h
		}
	}

	srv, err := server.New(server.Options{
		Listen:            ":0",
		Path:              "/lanchat",
		AuthMode:          authMode,
		PSKHash:           pskHash,
		HeartbeatInterval: time.Duration(protocol.HeartbeatIntervalSec) * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownGrace:     time.Second,
		RelayListen:       ":0",
		RelayHost:         relayHost,
	}, nil)
	if err != nil {
		return "", err
	}

	srvID := "auto-" + devID
	srvCtx, srvCancel := context.WithCancel(c.runCtx)
	c.mu.Lock()
	c.embeddedStop = func() {
		unregisterEmbeddedServer(srvID)
		srvCancel()
	}
	c.mu.Unlock()

	go func() { _ = srv.Serve(srvCtx) }()
	select {
	case <-srv.Ready():
		_, portStr, err := net.SplitHostPort(srv.ListenAddr())
		if err != nil {
			srvCancel()
			return "", err
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			srvCancel()
			return "", err
		}

		advAddr := net.JoinHostPort(relayHost, portStr)
		registerEmbeddedServer(appapi.Server{
			Name:     nick,
			ID:       srvID,
			Addr:     advAddr,
			Version:  protocol.ProtocolVersion,
			AuthMode: authMode,
		})

		adv, _ := discover.NewAdvertiser(port, discover.ServerMeta{
			Version: protocol.ProtocolVersion,
			Path:    "/lanchat",
			Name:    nick,
			Auth:    authMode,
			ID:      srvID,
			TLS:     false,
		})
		go func() {
			<-srvCtx.Done()
			unregisterEmbeddedServer(srvID)
			if adv != nil {
				adv.Close()
			}
		}()

		return "ws://" + net.JoinHostPort("127.0.0.1", portStr) + "/lanchat", nil
	case <-c.runCtx.Done():
		srvCancel()
		return "", c.runCtx.Err()
	}
}
