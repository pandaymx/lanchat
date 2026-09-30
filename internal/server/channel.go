package server

import (
	"strings"

	"github.com/pandaymx/lanchat/internal/group"
	"github.com/pandaymx/lanchat/internal/protocol"
)

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// channelSink 把 group.Registry 的频道变化桥接为信令下发。
// 所有方法都在 hub 单 goroutine 内被调用（Registry 仅被 hub 操作）。
type channelSink struct{ h *hub }

func (s channelSink) OnMatrix(_ group.MatrixEvent) {} // 文件 swarm 数据面在后续 PR 接线

// OnChannel 把频道列表变化下发给全部在线客户端（无 ReplyTo，视为主动更新）。
func (s channelSink) OnChannel(ev group.ChannelEvent) {
	if ev.Kind != group.ChannelChanged {
		return
	}
	s.h.pushChannels(s.toProtocol(ev.Channels), "")
}

func (s channelSink) toProtocol(in []group.ChannelInfo) []protocol.Channel {
	out := make([]protocol.Channel, 0, len(in))
	for _, c := range in {
		out = append(out, protocol.Channel{
			ID: c.ID, Name: c.Name, OwnerID: c.OwnerID, Members: c.Members,
		})
	}
	return out
}

// onCreate 处理 CHANNEL_CREATE：创建者成为 owner 与首个成员。
func (h *hub) onCreate(c *Client, env *protocol.Envelope) {
	var p protocol.ChannelCreatePayload
	if err := env.DecodePayload(&p); err != nil || isBlank(p.Name) {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_channel", Message: "频道名称非法",
		})
		return
	}
	if _, err := h.channels.Create(p.Name, c.id, false); err != nil {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_channel", Message: "频道创建失败",
		})
		return
	}
	h.ack(c, env.ID, "delivered")
}

// onJoin 处理 CHANNEL_JOIN：public 频道任意在线成员可加入。
func (h *hub) onJoin(c *Client, env *protocol.Envelope) {
	var p protocol.ChannelJoinPayload
	if err := env.DecodePayload(&p); err != nil {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "invalid_channel", Message: "频道加入消息非法",
		})
		return
	}
	if err := h.channels.Join(p.ChannelID, c.id); err != nil {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code: "channel_unavailable", Message: "频道不存在或不可加入",
		})
		return
	}
	h.ack(c, env.ID, "delivered")
}

// onChannelList 处理 CHANNEL_LIST 请求：仅向请求者回复（带 ReplyTo）。
func (h *hub) onChannelList(c *Client, env *protocol.Envelope) {
	list := channelSink{h: h}.toProtocol(h.channels.List())
	h.pushChannels(list, env.ID)
}

// pushChannels 下发 CHANNEL_LIST；replyTo 非空时表示对某次请求的应答。
func (h *hub) pushChannels(list []protocol.Channel, replyTo string) {
	env, err := protocol.NewEnvelope(newMsgID(), protocol.ChannelList, protocol.ChannelListPayload{
		Channels: list,
	})
	if err != nil {
		return
	}
	env.ReplyTo = replyTo
	_, users := h.reg.snapshot()
	for _, u := range users {
		if cli, ok := h.reg.get(u.ID); ok && !cli.enqueue(env) {
			h.drop(cli)
		}
	}
}
