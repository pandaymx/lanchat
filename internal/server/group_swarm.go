package server

import (
	"github.com/pandaymx/lanchat/internal/protocol"
)

// onGroupOffer 处理源发起的群组文件分发。
//
// 登记会话后把 GROUP_OFFER 转发给目标成员：G1 为全体在线成员（除源），
// G2 为频道成员（非成员发起被拒，消息仅成员可见）。各端收到后自行决定
// 是否加入，经 GROUP_JOIN 回传块端口候选与令牌。
func (h *hub) onGroupOffer(c *Client, env *protocol.Envelope) {
	var p protocol.GroupOfferPayload
	if err := env.DecodePayload(&p); err != nil ||
		p.TransferID == "" || p.Name == "" || p.Size < 0 || p.BlockCount < 0 {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_offer", Message: "群组文件要约非法",
		})
		return
	}

	var targets []string
	switch env.Group {
	case protocol.GroupBroadcast:
		_, users := h.reg.snapshot()
		for _, u := range users {
			if u.ID != c.id {
				targets = append(targets, u.ID)
			}
		}
	default:
		// G2 自定义频道：由频道注册表给出成员，非成员发起被拒。
		rcpt, err := h.channels.Recipients(env.Group, c.id)
		if err != nil {
			h.send(c, protocol.Error, protocol.ErrorPayload{
				Code: "not_channel_member", Message: "仅频道成员可向该频道发送文件",
			})
			return
		}
		for _, id := range rcpt {
			if id != c.id {
				targets = append(targets, id)
			}
		}
	}

	if _, ok := h.swarms.create(env.Group, p.TransferID, c.id); !ok {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_offer", Message: "该群组文件分发已存在",
		})
		return
	}

	env.From = c.id
	for _, id := range targets {
		h.relayTo(id, env)
	}
	h.ack(c, env.ID, "delivered")
}

// onGroupJoin 处理成员加入：登记候选后把 JOIN 转发给源与其余成员，
// 让所有成员都能建立到新成员的块连接；随后把已有成员的 JOIN 补发给
// 新成员，使其获得完整成员集合（含各自块候选）。
func (h *hub) onGroupJoin(c *Client, env *protocol.Envelope) {
	var p protocol.GroupJoinPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_join", Message: "群组加入消息非法",
		})
		return
	}
	g, ok := h.swarms.get(p.GroupID, p.TransferID)
	if !ok || g.source == c.id {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "swarm_unavailable", Message: "群组文件分发不存在或已结束",
		})
		return
	}
	if !g.join(c.id, p.Candidates, p.Token) {
		// 重复加入：幂等应答，不重复广播。
		h.ack(c, env.ID, "delivered")
		return
	}

	// 转发新成员 JOIN 给源与其余已有成员。
	env.From = c.id
	h.relayTo(g.source, env)
	for id := range g.members {
		if id != c.id {
			h.relayTo(id, env)
		}
	}

	// 补发已有成员 JOIN 给新成员（源不单独补：源的候选已在 GROUP_OFFER 中）。
	for id := range g.members {
		if id == c.id {
			continue
		}
		joinEnv, err := protocol.NewEnvelope(newMsgID(), protocol.GroupJoin, protocol.GroupJoinPayload{
			GroupID:    g.groupID,
			TransferID: g.transferID,
			Candidates: g.cand[id],
			Token:      g.token[id],
		})
		if err != nil {
			continue
		}
		joinEnv.From = id
		h.relayTo(c.id, joinEnv)
	}

	h.ack(c, env.ID, "delivered")
}

// onGroupLeave 处理成员主动离开：转发给源与其余成员。
func (h *hub) onGroupLeave(c *Client, env *protocol.Envelope) {
	var p protocol.GroupJoinPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_leave", Message: "群组离开消息非法",
		})
		return
	}
	g, ok := h.swarms.get(p.GroupID, p.TransferID)
	if !ok || g.source == c.id || !g.leave(c.id) {
		h.ack(c, env.ID, "undeliverable")
		return
	}
	env.From = c.id
	h.relayTo(g.source, env)
	for id := range g.members {
		h.relayTo(id, env)
	}
	h.ack(c, env.ID, "delivered")
}

// onGroupProgress 把成员块位图转发给源，用于成员矩阵统计；不回执。
func (h *hub) onGroupProgress(c *Client, env *protocol.Envelope) {
	var p protocol.GroupProgressPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" {
		return
	}
	g, ok := h.swarms.get(p.GroupID, p.TransferID)
	if !ok {
		return
	}
	env.From = c.id
	h.relayTo(g.source, env)
}

// notifyGroupOffline 在成员离线时通知相关会话：源离线则通知原成员分发
// 结束；普通成员离线则向源与其余成员转发其 GROUP_LEAVE。
func (h *hub) notifyGroupOffline(id string) {
	ended, left := h.swarms.removeClientAll(id)

	leavePayload := func(g *groupSession) protocol.GroupJoinPayload {
		return protocol.GroupJoinPayload{GroupID: g.groupID, TransferID: g.transferID}
	}

	for _, g := range ended {
		for mid := range g.members {
			env, err := protocol.NewEnvelope(newMsgID(), protocol.GroupLeave, leavePayload(g))
			if err != nil {
				continue
			}
			env.From = id
			h.relayTo(mid, env)
		}
	}
	for _, g := range left {
		env, err := protocol.NewEnvelope(newMsgID(), protocol.GroupLeave, leavePayload(g))
		if err != nil {
			continue
		}
		env.From = id
		h.relayTo(g.source, env)
		for mid := range g.members {
			h.relayTo(mid, env)
		}
	}
}
