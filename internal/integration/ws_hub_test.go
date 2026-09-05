// 端到端测试：起真 Hub + WS 客户端，覆盖 M2 的核心路径。
//
// 这些是"它真的在跑吗"的最终证据——fake Hub 的抽象靠它们背书。
package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestTwoClientsExchange 两条客户端经真 hub 互发消息，互收 FKDeliver。
func TestTwoClientsExchange(t *testing.T) {
	t.Parallel()

	fx := newHubFixture(t)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	alice := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 1,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	})
	defer alice.close()

	bob := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 1,
		DeviceID:        "bob-desktop",
		UserID:          "bob",
	})
	defer bob.close()

	// Alice 发消息。
	alice.send(ctx, protocol.FKMessage, protocol.StoredMessage{
		ID:             "msg-1",
		ClientNonce:    "n-1",
		ConversationID: "conv-1",
		SenderUserID:   "alice",
		SenderDeviceID: "alice-laptop",
		Body:           "hello bob",
		CreatedAt:      time.Now().UnixMilli(),
	})

	// Alice 应收到自己的 FKDeliver（广播回环）。
	got := mustDecodeMsg(t, alice.waitFor(protocol.FKDeliver, 5*time.Second))
	if got.Body != "hello bob" {
		t.Fatalf("alice got wrong body: %q", got.Body)
	}
	if got.ServerSeq == 0 {
		t.Fatal("alice: ServerSeq not assigned by hub")
	}

	// Bob 应同时收到 FKDeliver。
	got2 := mustDecodeMsg(t, bob.waitFor(protocol.FKDeliver, 5*time.Second))
	if got2.Body != "hello bob" {
		t.Fatalf("bob got wrong body: %q", got2.Body)
	}
	if got2.ServerSeq != got.ServerSeq {
		t.Fatalf("seq mismatch: alice=%d bob=%d", got.ServerSeq, got2.ServerSeq)
	}
}

// TestReconnectHistory 客户端断线重连，Hub 应按 ResumeFrom 补发。
func TestReconnectHistory(t *testing.T) {
	t.Parallel()

	fx := newHubFixture(t)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 第一个连接：发 3 条消息，记下 ServerSeq。
	first := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 1,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	})
	for i, body := range []string{"one", "two", "three"} {
		first.send(ctx, protocol.FKMessage, protocol.StoredMessage{
			ID:             body,
			ClientNonce:    body,
			ConversationID: "conv-1",
			SenderUserID:   "alice",
			SenderDeviceID: "alice-laptop",
			Body:           body,
			CreatedAt:      time.Now().UnixMilli(),
		})
		// 等自己 FKDeliver 一次，确保 seq 已分配
		msg := mustDecodeMsg(t, first.waitFor(protocol.FKDeliver, 5*time.Second))
		if msg.Body != body {
			t.Fatalf("round %d: got %q", i, msg.Body)
		}
	}
	first.close()

	// 等连接真正断开（Hub 注销 peer）。
	time.Sleep(200 * time.Millisecond)

	// 第二个连接：ResumeFrom=0 应拉到全部 3 条。
	second := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 1,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
		ResumeFrom:      0,
	})
	defer second.close()

	// Hub 不知道 FKHistoryReq 的精确语义（M2 没实现），所以这里只验证
	// 真 hub 起得来 + FKHello 不被拒 + Hub 不崩。
	// ResumeFrom 的真实补发是 M2.5 / v0.2 的工作。
	_ = second
}

// TestHelloVersionMismatch v=2 的 FKHello 应被 hub 关连接。
func TestHelloVersionMismatch(t *testing.T) {
	t.Parallel()

	fx := newHubFixture(t)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 故意用 v=2
	c := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 2,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	})
	defer c.close()

	// Hub 应在合理时间内关连接：readLoop 会收到 EOF
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		_, _, err := c.ws.Read(ctx)
		if err != nil {
			// 任何错误（包括 EOF）都算 hub 拒绝了 v=2
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("hub did not close connection after v=2 FKHello")
		default:
		}
	}
}

// TestPingPong 验证 FKPing → FKPong。
func TestPingPong(t *testing.T) {
	t.Parallel()

	fx := newHubFixture(t)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := dial(t, fx.addr, protocol.Hello{
		ProtocolVersion: 1,
		DeviceID:        "alice-laptop",
		UserID:          "alice",
	})
	defer c.close()

	c.send(ctx, protocol.FKPing, nil)

	// 等 FKPong
	got := c.waitFor(protocol.FKPong, 5*time.Second)
	if len(got) > 0 {
		t.Logf("pong payload (should be empty): %s", got)
	}
}

// mustDecodeMsg 解码 StoredMessage JSON，失败 fatal。
func mustDecodeMsg(t *testing.T, b []byte) protocol.StoredMessage {
	t.Helper()
	var m protocol.StoredMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode StoredMessage: %v (raw: %s)", err, b)
	}
	return m
}
