package server

import (
	"context"
	"time"

	"github.com/pandaymx/lanchat/internal/group"
	"github.com/pandaymx/lanchat/internal/protocol"
)

type hubEventKind int

const (
	evRegisterSnapshot hubEventKind = iota + 1
	evUnregister
	evRoute
	evListReq
	evStatusSnapshot
)

// hubEvent 是投递给 hub 的事件。
//   - unregister：client 为来源；
//   - route/listReq：client 为来源，env 为消息；
//   - registerSnapshot：同步完成注册，reply 回传注册后的 revision 与全量在线表；
//   - statusSnapshot：查询当前在线成员、频道与中继/Swarm 状态快照。
type hubEvent struct {
	kind        hubEventKind
	client      *Client
	env         *protocol.Envelope
	reply       chan queryResult
	statusReply chan StatusSnapshot
}

// hub 是单 goroutine 的信令中枢：唯一持有在线表，串行处理所有事件。
type hub struct {
	opts   Options
	reg    *registry
	auth   *authenticator
	events chan hubEvent
	logf   func(string, ...any)

	relaySrv  relayServer // 中继数据面（可为 nil，表示未启用）
	relayHost string      // 宣告给客户端的中继主机

	channels *group.Registry // G2 自定义频道注册表

	swarms *swarmSessions // 群组文件分发会话表（服务端 swarm 无感，仅转发）
	spool  *messageSpool  // M16 离线消息环形暂存与频道历史快照
}

// relayServer 是中继数据面的最小依赖接口。
type relayServer interface {
	AdvertiseAddr(host string) string
}

func newHub(opts Options, logf func(string, ...any)) *hub {
	h := &hub{
		opts:      opts,
		reg:       newRegistry(),
		auth:      newAuthenticator(opts.AuthMode, opts.PSKHash),
		events:    make(chan hubEvent, 256),
		logf:      logf,
		relayHost: opts.RelayHost,
	}
	h.channels = group.NewRegistry(channelSink{h: h})
	h.swarms = newSwarmSessions()
	h.spool = newMessageSpool()
	return h
}

// queryResult 承载一次快照查询的结果。
type queryResult struct {
	revision uint64
	users    []protocol.User
	offline  []*protocol.Envelope
}

// registerSnapshot 同步完成注册并返回注册后的 revision、全量在线表与待补发的离线暂存消息，
// 供握手阶段在发 USER_LIST 前调用，保证首个客户端的在线表也含自己。
func (h *hub) registerSnapshot(ctx context.Context, c *Client) (uint64, []protocol.User, []*protocol.Envelope) {
	ch := make(chan queryResult, 1)
	ev := hubEvent{kind: evRegisterSnapshot, client: c, reply: ch}
	select {
	case h.events <- ev:
	case <-ctx.Done():
		return 0, nil, nil
	}
	select {
	case r := <-ch:
		return r.revision, r.users, r.offline
	case <-ctx.Done():
		return 0, nil, nil
	}
}

// submit 投递事件；hub 已关停时丢弃。
func (h *hub) submit(ev hubEvent) {
	select {
	case h.events <- ev:
	default:
		// 队列极不可能满；满了只能放弃（对端读/写泵会自行收敛）。
		if ev.client != nil {
			ev.client.terminate()
		}
	}
}

// run 是 hub 的事件循环，随 ctx 取消退出。
func (h *hub) run(ctx context.Context) {
	sweep := time.NewTicker(time.Second)
	defer sweep.Stop()

	for {
		select {
		case <-ctx.Done():
			h.broadcastShutdown()
			return
		case ev := <-h.events:
			h.handle(ev)
		case now := <-sweep.C:
			h.sweep(now)
		}
	}
}

func (h *hub) handle(ev hubEvent) {
	switch ev.kind {
	case evRegisterSnapshot:
		rev := h.onRegister(ev.client)
		_, users := h.reg.snapshot()
		var offline []*protocol.Envelope
		if h.spool != nil {
			offline = h.spool.drainUser(ev.client.id, h.now())
		}
		ev.reply <- queryResult{revision: rev, users: users, offline: offline}
	case evUnregister:
		h.onUnregister(ev.client)
	case evListReq:
		h.onListReq(ev.client)
	case evRoute:
		h.route(ev.client, ev.env)
	case evStatusSnapshot:
		rev, users := h.reg.snapshot()
		channels := channelSink{h: h}.toProtocol(h.channels.List())
		ev.statusReply <- StatusSnapshot{
			ProtocolVersion: protocol.ProtocolVersion,
			AuthMode:        h.opts.AuthMode,
			RelayEnabled:    h.opts.RelayListen != "",
			Revision:        rev,
			OnlineCount:     len(users),
			Users:           users,
			ChannelCount:    len(channels),
			Channels:        channels,
			ActiveSwarms:    len(h.swarms.sessions),
		}
	}
}

// statusSnapshot 同步向 hub 查询当前运行状态快照。
func (h *hub) statusSnapshot(ctx context.Context, startedAt time.Time) StatusSnapshot {
	ch := make(chan StatusSnapshot, 1)
	ev := hubEvent{kind: evStatusSnapshot, statusReply: ch}
	select {
	case h.events <- ev:
	case <-ctx.Done():
		return StatusSnapshot{ProtocolVersion: protocol.ProtocolVersion, AuthMode: h.opts.AuthMode}
	}
	select {
	case snap := <-ch:
		if !startedAt.IsZero() {
			snap.UptimeSec = int64(time.Since(startedAt).Seconds())
		}
		return snap
	case <-ctx.Done():
		return StatusSnapshot{ProtocolVersion: protocol.ProtocolVersion, AuthMode: h.opts.AuthMode}
	}
}

// onRegister 处理新连接注册并返回注册后的 revision；同 ID 旧会话先被顶掉（不增加 revision）。
func (h *hub) onRegister(c *Client) uint64 {
	if old, ok := h.reg.get(c.id); ok && old != c {
		old.replaced = true
		h.reg.replace(c)
		old.terminate()
		c.registered = true
		return h.reg.revision
	}
	rev := h.reg.add(c)
	c.registered = true
	h.broadcastJoin(c, rev)
	return rev
}

func (h *hub) onUnregister(c *Client) {
	cur, ok := h.reg.get(c.id)
	if !ok || cur != c {
		// 已被同 ID 新会话替换，或此前已移除：不再广播。
		return
	}
	_, rev, _ := h.reg.remove(c.id)
	// 清理该成员的全部频道关系；Registry 会经 channelSink 下发更新后的频道列表。
	h.channels.Remove(c.id)
	// 清理该成员涉及的群组文件分发会话并通知其余成员。
	h.notifyGroupOffline(c.id)
	payload := protocol.UserUpdatePayload{User: c.user(), Revision: rev}
	h.fanoutExcept(c, protocol.UserLeave, payload)
}

func (h *hub) onListReq(c *Client) {
	rev, users := h.reg.snapshot()
	h.send(c, protocol.UserList, protocol.UserListPayload{Revision: rev, Users: users})
}

// sweep 淘汰超过 IdleTimeout 无任何帧的客户端，并清理过期离线暂存。
func (h *hub) sweep(now time.Time) {
	if h.spool != nil {
		h.spool.prune(now)
	}
	cutoff := now.Add(-h.opts.IdleTimeout)
	for _, c := range h.reg.idleClients(cutoff) {
		h.logf("客户端 %s 超过 %s 无活动，断开", c.id, h.opts.IdleTimeout)
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "idle_timeout",
			Message: "连接因空闲超时被关闭",
		})
		h.onUnregister(c)
		c.terminate()
	}
}

func (h *hub) broadcastShutdown() {
	payload := protocol.ServerShutdownPayload{
		GracePeriodSec: int(h.opts.ShutdownGrace / time.Second),
	}
	_, users := h.reg.snapshot()
	for _, u := range users {
		if c, ok := h.reg.get(u.ID); ok {
			h.send(c, protocol.ServerShutdown, payload)
		}
	}
}

// send 向单个客户端非阻塞下发消息，失败则踢除慢客户端。
func (h *hub) send(c *Client, typ string, payload any) {
	env, err := protocol.NewEnvelope(newMsgID(), typ, payload)
	if err != nil {
		return
	}
	if !c.enqueue(env) {
		h.drop(c)
	}
}

// drop 踢除慢客户端：终止连接并按正常下线处理。
func (h *hub) drop(c *Client) {
	if cur, ok := h.reg.get(c.id); ok && cur == c {
		_, rev, _ := h.reg.remove(c.id)
		h.channels.Remove(c.id)
		h.notifyGroupOffline(c.id)
		h.fanoutExcept(c, protocol.UserLeave, protocol.UserUpdatePayload{
			User: c.user(), Revision: rev,
		})
	}
	c.terminate()
}

// fanoutExcept 向除 skip 外的所有在线客户端广播一条增量事件。
func (h *hub) fanoutExcept(skip *Client, typ string, payload any) {
	env, err := protocol.NewEnvelope(newMsgID(), typ, payload)
	if err != nil {
		return
	}
	_, users := h.reg.snapshot()
	for _, u := range users {
		if u.ID == skip.id {
			continue
		}
		if c, ok := h.reg.get(u.ID); ok && !c.enqueue(env) {
			h.drop(c)
		}
	}
}

func (h *hub) broadcastJoin(c *Client, rev uint64) {
	h.fanoutExcept(c, protocol.UserJoin, protocol.UserUpdatePayload{
		User: c.user(), Revision: rev,
	})
}
