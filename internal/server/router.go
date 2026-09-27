package server

import (
	"time"
	"unicode/utf8"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// 业务层消息长度上限。
const (
	maxTextBytes    = 4 << 10   // 文本 4 KiB
	maxStickerBytes = 256 << 10 // 表情内联 base64 256 KiB
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
	case protocol.FileOffer, protocol.FileAccept, protocol.FileReject, protocol.FileCancel:
		h.forward(c, env)
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
	h.forward(c, env)
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
