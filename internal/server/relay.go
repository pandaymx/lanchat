// 本文件实现信令服务器对中继会话的编排：接收 RELAY_REQUEST，分配 relayID，
// 向双方分别下发 RELAY_GRANT，并转发 RELAY_KEY。服务器不持有文件密钥，
// 中继数据面只透传端到端密文。
package server

import (
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// onRelayRequest 处理一方发起的中继请求：生成 relayID，向请求方与对端发 grant。
func (h *hub) onRelayRequest(c *Client, env *protocol.Envelope) {
	c.touch()

	var p protocol.RelayRequestPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" || p.PeerID == "" {
		h.send(c, protocol.Error, protocol.ErrorPayload{
			Code:    "invalid_relay_request",
			Message: "RELAY_REQUEST 缺少 transferID 或 peerID",
		})
		return
	}

	peer, ok := h.reg.get(p.PeerID)
	if !ok {
		h.ack(c, env.ID, "undeliverable")
		return
	}

	relayID := uuid.NewString()
	relayAddr := ""
	if h.relaySrv != nil {
		relayAddr = h.relaySrv.AdvertiseAddr(h.relayHost)
	}

	// 双方收到的 grant 各自标明对端成员，客户端据此路由到对应群任务
	// （1:1 路径下 PeerID 仅为附加信息，不影响既有行为）。
	h.send(peer, protocol.RelayGrant, protocol.RelayGrantPayload{
		TransferID: p.TransferID,
		RelayID:    relayID,
		RelayAddr:  relayAddr,
		PeerID:     c.id,
	})
	h.send(c, protocol.RelayGrant, protocol.RelayGrantPayload{
		TransferID: p.TransferID,
		RelayID:    relayID,
		RelayAddr:  relayAddr,
		PeerID:     p.PeerID,
	})
}
