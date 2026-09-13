// mesh_test.go 验证双节点收敛（ADR-014 wire v2）。
//
// 用两个临时 libsql store 模拟两个 mesh 节点：各自写本地消息后
// 互相同步三轮，断言最终消息集一致（幂等去重不重复落库）。
package mesh

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
)

func openMeshStore(t *testing.T) *libsql.Store {
	t.Helper()
	ctx := context.Background()
	s, err := libsql.Open(ctx, "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// stubRemote 把 Sync 路由到远端 store 的 Respond 逻辑（无网络）。
type stubRemote struct {
	store SourceStore
}

func (r stubRemote) Sync(ctx context.Context, req protocol.SyncRequest) ([]protocol.SyncResponse, error) {
	return Respond(ctx, r.store, req)
}

func localMsg(nodeID string, seq uint64, conv, id, body string) protocol.StoredMessage {
	return protocol.StoredMessage{
		ID:             id,
		ConversationID: conv,
		SenderUserID:   "alice",
		SenderDeviceID: "dev-" + nodeID,
		Body:           body,
		ServerSeq:      seq,
		CreatedAt:      int64(seq) * 1000,
		NodeID:         nodeID,
	}
}

func msgSet(t *testing.T, s *libsql.Store) map[string]bool {
	t.Helper()
	ctx := context.Background()
	cursor, err := s.SourceCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for node := range cursor {
		msgs, err := s.SyncMessages(ctx, node, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			set[node+"#"+m.Body] = true
		}
	}
	return set
}

// TestTwoNodesConverge：A/B 各有本地消息，互相同步三轮后全量一致。
func TestTwoNodesConverge(t *testing.T) {
	ctx := context.Background()
	a := openMeshStore(t)
	b := openMeshStore(t)

	// A 本地两条，B 本地一条。
	for _, m := range []protocol.StoredMessage{
		localMsg("node-a", 1, "conv-1", "a1", "hello from a1"),
		localMsg("node-a", 2, "conv-1", "a2", "hello from a2"),
	} {
		if err := a.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.AppendMessage(ctx, localMsg("node-b", 1, "conv-1", "b1", "hello from b1")); err != nil {
		t.Fatal(err)
	}

	// 同步三轮（每轮 A←B 和 B←A 各一次）。
	for i := 0; i < 3; i++ {
		if n, err := PullOnce(ctx, a, stubRemote{b}); err != nil || n < 0 {
			t.Fatalf("round %d A<-B: n=%d err=%v", i, n, err)
		}
		if n, err := PullOnce(ctx, b, stubRemote{a}); err != nil || n < 0 {
			t.Fatalf("round %d B<-A: n=%d err=%v", i, n, err)
		}
	}

	// 收敛：两边消息集一致，且各有 3 条（不重复）。
	sa, sb := msgSet(t, a), msgSet(t, b)
	if len(sa) != 3 {
		t.Fatalf("node-a has %d messages, want 3: %v", len(sa), sa)
	}
	if len(sb) != 3 {
		t.Fatalf("node-b has %d messages, want 3: %v", len(sb), sb)
	}
	for k := range sa {
		if !sb[k] {
			t.Fatalf("node-b missing %q", k)
		}
	}
}

// TestPullOnceIdempotent：重复同步不重复落库（幂等）。
func TestPullOnceIdempotent(t *testing.T) {
	ctx := context.Background()
	a := openMeshStore(t)
	b := openMeshStore(t)

	if err := b.AppendMessage(ctx, localMsg("node-b", 1, "conv-1", "b1", "hi")); err != nil {
		t.Fatal(err)
	}
	// 第一轮拉到 1 条。
	n, err := PullOnce(ctx, a, stubRemote{b})
	if err != nil || n != 1 {
		t.Fatalf("first pull: n=%d err=%v", n, err)
	}
	// 第二轮无新消息。
	n, err = PullOnce(ctx, a, stubRemote{b})
	if err != nil || n != 0 {
		t.Fatalf("second pull: n=%d err=%v", n, err)
	}
	if got := len(msgSet(t, a)); got != 1 {
		t.Fatalf("node-a has %d messages after idempotent pull, want 1", got)
	}
}

// TestRespondBatchLimit：增量超过单批上限时 More=true 且可续拉。
func TestRespondBatchLimit(t *testing.T) {
	ctx := context.Background()
	a := openMeshStore(t)

	for i := 1; i <= 5; i++ {
		m := localMsg("node-a", uint64(i), "conv-1", "m", "batch")
		m.ID = "m" + string(rune('0'+i))
		m.Body = "batch " + string(rune('0'+i))
		if err := a.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	// 单批上限 3：第一批 More=true。
	resps, err := Respond(ctx, a, protocol.SyncRequest{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	if len(resps[0].Messages) != 3 || !resps[0].More {
		t.Fatalf("first batch = %d msgs more=%v, want 3 true", len(resps[0].Messages), resps[0].More)
	}

	// 续拉：游标 = 第一批最大 seq。
	cursor := map[string]uint64{"node-a": resps[0].Messages[2].ServerSeq}
	resps2, err := Respond(ctx, a, protocol.SyncRequest{Cursor: cursor, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resps2) != 1 || len(resps2[0].Messages) != 2 || resps2[0].More {
		t.Fatalf("second batch = %+v, want 2 msgs more=false", resps2)
	}
}
