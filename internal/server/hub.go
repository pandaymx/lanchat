package server

import (
	"context"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

type hubEventKind int

const (
	evRegisterSnapshot hubEventKind = iota + 1
	evUnregister
	evRoute
	evListReq
)

// hubEvent 是投递给 hub 的事件。
//   - unregister：client 为来源；
//   - route/listReq：client 为来源，env 为消息；
//   - registerSnapshot：同步完成注册，reply 回传注册后的 revision 与全量在线表。
type hubEvent struct {
	kind   hubEventKind
	client *Client
	env    *protocol.Envelope
	reply  chan queryResult
}

// hub 是单 goroutine 的信令中枢：唯一持有在线表，串行处理所有事件。
type hub struct {
	opts   Options
	reg    *registry
	auth   *authenticator
	events chan hubEvent
	logf   func(string, ...any)
}

func newHub(opts Options, logf func(string, ...any)) *hub {
	return &hub{
		opts:   opts,
		reg:    newRegistry(),
		auth:   newAuthenticator(opts.AuthMode, opts.PSKHash),
		events: make(chan hubEvent, 256),
		logf:   logf,
	}
}

// queryResult 承载一次快照查询的结果。
type queryResult struct {
	revision uint64
	users    []protocol.User
}

// registerSnapshot 同步完成注册并返回注册后的 revision 与全量在线表，
// 供握手阶段在发 USER_LIST 前调用，保证首个客户端的在线表也含自己。
func (h *hub) registerSnapshot(ctx context.Context, c *Client) (uint64, []protocol.User) {
	ch := make(chan queryResult, 1)
	ev := hubEvent{kind: evRegisterSnapshot, client: c, reply: ch}
	select {
	case h.events <- ev:
	case <-ctx.Done():
		return 0, nil
	}
	select {
	case r := <-ch:
		return r.revision, r.users
	case <-ctx.Done():
		return 0, nil
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
		ev.reply <- queryResult{revision: rev, users: users}
	case evUnregister:
		h.onUnregister(ev.client)
	case evListReq:
		h.onListReq(ev.client)
	case evRoute:
		h.route(ev.client, ev.env)
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
	payload := protocol.UserUpdatePayload{User: c.user(), Revision: rev}
	h.fanoutExcept(c, protocol.UserLeave, payload)
}

func (h *hub) onListReq(c *Client) {
	rev, users := h.reg.snapshot()
	h.send(c, protocol.UserList, protocol.UserListPayload{Revision: rev, Users: users})
}

// sweep 淘汰超过 IdleTimeout 无任何帧的客户端。
func (h *hub) sweep(now time.Time) {
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
