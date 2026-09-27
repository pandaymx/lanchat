package integration

import (
	"testing"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// TestFileSignalingForwarded 验证四类文件信令经中心节点透传：
// 接收方收到的消息 From 被强制盖为发送者，发送者收到 delivered ACK；
// 发给离线/不存在用户时得到 undeliverable ACK。
func TestFileSignalingForwarded(t *testing.T) {
	url := startTestServer(t)
	a := dialClient(t, url, testPSK, "Alice")
	b := dialClient(t, url, testPSK, "Bob")

	t.Run("offer delivered and stamped", func(t *testing.T) {
		offer := a.sendTo(protocol.FileOffer, b.id, protocol.FileOfferPayload{
			TransferID: "tx-1",
			Name:       "report.bin",
			Size:       4096,
			ChunkSize:  1 << 20,
			SHA256:     "abc123",
		})

		got := b.recvUntil(protocol.FileOffer, readTimeout)
		if got.From != a.id {
			t.Fatalf("From = %q，期望 %q", got.From, a.id)
		}
		var p protocol.FileOfferPayload
		if err := got.DecodePayload(&p); err != nil {
			t.Fatal(err)
		}
		if p.TransferID != "tx-1" || p.Size != 4096 {
			t.Fatalf("负载透传异常: %+v", p)
		}

		ack := a.recvUntil(protocol.MsgAck, readTimeout)
		var ap protocol.MsgAckPayload
		if err := ack.DecodePayload(&ap); err != nil {
			t.Fatal(err)
		}
		if ap.Status != "delivered" || ack.ReplyTo != offer.ID {
			t.Fatalf("ACK = status %q replyTo %q，期望 delivered / %s", ap.Status, ack.ReplyTo, offer.ID)
		}
	})

	t.Run("cancel delivered and stamped", func(t *testing.T) {
		a.sendTo(protocol.FileCancel, b.id, protocol.FileCancelPayload{TransferID: "tx-1"})

		got := b.recvUntil(protocol.FileCancel, readTimeout)
		if got.From != a.id {
			t.Fatalf("From = %q，期望 %q", got.From, a.id)
		}
	})

	t.Run("accept and reject forwarded", func(t *testing.T) {
		b.sendTo(protocol.FileAccept, a.id, protocol.FileAcceptPayload{TransferID: "tx-2"})
		if got := a.recvUntil(protocol.FileAccept, readTimeout); got.From != b.id {
			t.Fatalf("ACCEPT From = %q，期望 %q", got.From, b.id)
		}

		b.sendTo(protocol.FileReject, a.id, protocol.FileRejectPayload{TransferID: "tx-3", Reason: "busy"})
		if got := a.recvUntil(protocol.FileReject, readTimeout); got.From != b.id {
			t.Fatalf("REJECT From = %q，期望 %q", got.From, b.id)
		}
	})

	t.Run("offer to offline is undeliverable", func(t *testing.T) {
		offer := a.sendTo(protocol.FileOffer, "nonexistent-id", protocol.FileOfferPayload{
			TransferID: "tx-4",
			Name:       "x.bin",
			Size:       10,
		})
		ack := a.recvUntil(protocol.MsgAck, readTimeout)
		var p protocol.MsgAckPayload
		if err := ack.DecodePayload(&p); err != nil {
			t.Fatal(err)
		}
		if p.Status != "undeliverable" || ack.ReplyTo != offer.ID {
			t.Fatalf("ACK = status %q replyTo %q，期望 undeliverable / %s", p.Status, ack.ReplyTo, offer.ID)
		}
	})
}
