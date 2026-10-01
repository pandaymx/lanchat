package server

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// 业务层消息长度上限。
const (
	maxTextBytes     = 4 << 10   // 文本 4 KiB
	maxStickerBytes  = 256 << 10 // 表情内联 base64 256 KiB
	maxNicknameBytes = 64        // 昵称 64 字节
)

// route 在 hub goroutine 内处理注册客户端上行的各类消息。
func (h *hub) route(c *Client, env *protocol.Envelope) {
	c.touch()

	switch env.Type {
	case protocol.Heartbeat:
		h.send(c, protocol.HeartbeatAck, protocol.HeartbeatPayload{
			Revision:   h.reg.revision,
			ServerTime: h.now().UnixMilli(),
		})
	case protocol.UserListReq:
		h.onListReq(c)
	case protocol.TextMsg:
		h.routeText(c, env)
	case protocol.StickerMsg:
		h.routeSticker(c, env)
	case protocol.Typing:
		h.forward(c, env)
	case protocol.FileOffer, protocol.FileAccept, protocol.FileReject, protocol.FileCancel, protocol.FileReverse:
		h.forward(c, env)
	case protocol.GroupOffer:
		h.onGroupOffer(c, env)
	case protocol.GroupJoin:
		h.onGroupJoin(c, env)
	case protocol.GroupLeave:
		h.onGroupLeave(c, env)
	case protocol.GroupProgress:
		h.onGroupProgress(c, env)
	case protocol.ChannelCreate:
		h.onCreate(c, env)
	case protocol.ChannelJoin:
		h.onJoin(c, env)
	case protocol.ChannelList:
		h.onChannelList(c, env)
	case protocol.RelayRequest:
		h.onRelayRequest(c, env)
	case protocol.RelayKey:
		h.forward(c, env)
	case protocol.PresenceUpdate:
		h.onPresenceUpdate(c, env)
	default:
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "unknown_type",
			Message: "未知或不支持的消息类型",
		})
	}
}

func (h *hub) routeText(c *Client, env *protocol.Envelope) {
	var p protocol.TextPayload
	if err := env.DecodePayload(&p); err != nil || !utf8.ValidString(p.Text) || len(p.Text) > maxTextBytes {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "invalid_text",
			Message: "文本非法或超过 4 KiB 上限",
		})
		return
	}

	switch env.Group {
	case protocol.GroupUnicast:
		h.forward(c, env)
	case protocol.GroupBroadcast:
		h.fanoutGroup(c, env, nil)
	default:
		// G2 自定义频道：由频道注册表给出成员，非成员发送被拒（消息仅成员可见）。
		rcpt, err := h.channels.Recipients(env.Group, c.id)
		if err != nil {
			h.send(c, protocol.Error, protocol.ErrorPayload{
				Code:    "not_channel_member",
				Message: "仅频道成员可向该频道发消息",
			})
			return
		}
		h.fanoutGroup(c, env, rcpt)
	}
}

// fanoutGroup 把已盖发送者印章的消息分发给 targets。
// targets 为 nil 表示 G1 全体（除发送者外的所有在线客户端）。
func (h *hub) fanoutGroup(c *Client, env *protocol.Envelope, targets []string) {
	env.From = c.id
	if targets == nil {
		_, users := h.reg.snapshot()
		targets = make([]string, 0, len(users))
		for _, u := range users {
			if u.ID != c.id {
				targets = append(targets, u.ID)
			}
		}
	}
	delivered := 0
	for _, id := range targets {
		target, ok := h.reg.get(id)
		if !ok {
			continue
		}
		if target.enqueue(env) {
			delivered++
		} else {
			h.drop(target)
		}
	}
	status := "delivered"
	if delivered == 0 {
		status = "undeliverable"
	}
	h.ack(c, env.ID, status)
}

func (h *hub) routeSticker(c *Client, env *protocol.Envelope) {
	var p protocol.StickerPayload
	if err := env.DecodePayload(&p); err != nil || len(p.B64) > maxStickerBytes {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "invalid_sticker",
			Message: "表情非法或内联数据超过 256 KiB 上限",
		})
		return
	}
	h.forward(c, env)
}

// onPresenceUpdate 处理客户端资料变更：更新昵称，bump revision 后广播给其他人。
func (h *hub) onPresenceUpdate(c *Client, env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "invalid_presence",
			Message: "资料变更消息非法",
		})
		return
	}
	nick := strings.TrimSpace(p.User.Nickname)
	if nick == "" || len(nick) > maxNicknameBytes {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "invalid_presence",
			Message: "昵称非法或超过长度上限",
		})
		return
	}
	c.nickname = nick
	rev := h.reg.bumpRevision()
	h.fanoutExcept(c, protocol.PresenceUpdate, protocol.UserUpdatePayload{
		User: c.user(), Revision: rev,
	})
	h.ack(c, env.ID, "delivered")
}

// forward 把消息强制盖发送者印章后单播给 env.To，并向发送者回 MSG_ACK。
func (h *hub) forward(c *Client, env *protocol.Envelope) {
	env.From = c.id // 客户端不可伪造发送者

	target, ok := h.reg.get(env.To)
	if env.To == "" || !ok {
		h.ack(c, env.ID, "undeliverable")
		return
	}

	if !target.enqueue(env) {
		h.ack(c, env.ID, "undeliverable")
		h.drop(target)
		return
	}
	if env.Type != protocol.Typing {
		h.ack(c, env.ID, "delivered")
	}
}

func (h *hub) ack(c *Client, replyTo, status string) {
	env, err := protocol.NewEnvelope(newMsgID(), protocol.MsgAck, protocol.MsgAckPayload{Status: status})
	if err != nil {
		return
	}
	env.ReplyTo = replyTo
	if !c.enqueue(env) {
		h.drop(c)
	}
}

func (h *hub) now() time.Time { return time.Now() }
