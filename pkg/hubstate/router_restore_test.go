package hubstate

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// TestRouterRestoreFromStore 验证 Hub 重启恢复配方（M5，与 cmd/hub 启动序列同构）：
// 第一世 Router 落库 3 条消息后「重启」——第二世 Router 以已落库最大 seq 为
// StartSeq，并把 Store 里的最近消息灌回内存补发缓冲。期望：
//  1. 重连设备的 FKHistoryReq 能补到重启前的 3 条（补发缓冲已恢复）；
//  2. 重启后的新消息序号接在历史之后（StartSeq 生效，不与历史撞号）。
func TestRouterRestoreFromStore(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })

	// 第一世：3 条消息落库。
	r1 := NewRouter(context.Background(), &RouterConfig{Store: store})
	p1, id1 := addPeer(t, r1, "dev-1", "u-1")
	for i := 1; i <= 3; i++ {
		msg := protocol.StoredMessage{
			ID:             fmt.Sprintf("m%d", i),
			ConversationID: "",
			SenderUserID:   "u-1",
			Body:           fmt.Sprintf("body-%d", i),
		}
		if err := r1.HandleFrame(ctx, id1, p1,
			protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg)}); err != nil {
			t.Fatalf("first life send %d: %v", i, err)
		}
	}
	if !p1.waitFor(protocol.FKDeliver, 3, time.Second) {
		t.Fatal("first life: 3 deliveries missing")
	}
	r1.Close()

	// 重启恢复（与 cmd/hub openStore 之后的步骤同构）。
	prior, err := store.History(ctx, "", 0, HistoryRestoreLimit)
	if err != nil {
		t.Fatalf("load prior history: %v", err)
	}
	if len(prior) != 3 {
		t.Fatalf("store 中应有 3 条历史，实际 %d", len(prior))
	}
	var maxSeq uint64
	for _, m := range prior {
		if m.ServerSeq > maxSeq {
			maxSeq = m.ServerSeq
		}
	}
	r2 := NewRouter(context.Background(), &RouterConfig{Store: store, StartSeq: maxSeq})
	t.Cleanup(r2.Close)
	for i := range prior {
		r2.History().Append(prior[i])
	}

	// 重连设备补发：after=0 应拿回重启前的 3 条。
	p2, id2 := addPeer(t, r2, "dev-2", "u-2")
	req := protocol.HistoryRequest{ConversationIDs: []string{""}, After: 0, Limit: 100}
	if err := r2.HandleFrame(ctx, id2, p2,
		protocol.Frame{Kind: protocol.FKHistoryReq, Payload: mustPayload(t, req)}); err != nil {
		t.Fatalf("history req after restart: %v", err)
	}
	if !p2.waitFor(protocol.FKHistoryResp, 1, time.Second) {
		t.Fatal("reconnected peer: no FKHistoryResp")
	}
	var resp protocol.HistoryResponse
	if err := json.Unmarshal(p2.framesOf(protocol.FKHistoryResp)[0].Payload, &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("补发应含重启前 3 条，实际 %d", len(resp.Messages))
	}

	// 重启后新消息序号接续：应为 maxSeq+1。
	msg4 := protocol.StoredMessage{ID: "m4", ConversationID: "", SenderUserID: "u-2", Body: "after-restart"}
	if err := r2.HandleFrame(ctx, id2, p2,
		protocol.Frame{Kind: protocol.FKMessage, Payload: mustPayload(t, msg4)}); err != nil {
		t.Fatalf("second life send: %v", err)
	}
	if !p2.waitFor(protocol.FKDeliver, 1, time.Second) {
		t.Fatal("second life: no FKDeliver")
	}
	deliv := p2.framesOf(protocol.FKDeliver)
	var got protocol.StoredMessage
	if err := json.Unmarshal(deliv[len(deliv)-1].Payload, &got); err != nil {
		t.Fatalf("unmarshal deliver: %v", err)
	}
	if got.ServerSeq != maxSeq+1 {
		t.Fatalf("重启后新消息 seq 应为 %d（接续历史），实际 %d", maxSeq+1, got.ServerSeq)
	}
}
