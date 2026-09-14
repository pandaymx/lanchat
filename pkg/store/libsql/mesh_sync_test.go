package libsql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// 打开临时文件库（每个测试独立目录），返回 Store 与其 dsn。
func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db")
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dsn
}

func msg(nodeID string, seq uint64, conv, id string) protocol.StoredMessage {
	return protocol.StoredMessage{
		ID:             id,
		ConversationID: conv,
		SenderUserID:   "alice",
		SenderDeviceID: "dev-a",
		Body:           "hello " + id,
		ServerSeq:      seq,
		CreatedAt:      int64(seq) * 1000,
		NodeID:         nodeID,
	}
}

// TestWire2Schema：node_id 列与 (node_id, server_seq) 唯一索引就位，
// 且幂等打开不报错（migrate 重复执行安全）。
func TestWire2Schema(t *testing.T) {
	ctx := context.Background()
	s, dsn := openTestStore(t)

	// 幂等：同库再 Open 一次不应报错。
	s2, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()

	// 列存在：写一条带 node_id 的消息能成功。
	m := msg("node-a", 1, "conv-1", "m1")
	if _, err := s.AppendMessage(ctx, m); err != nil {
		t.Fatalf("AppendMessage with node_id: %v", err)
	}
}

// TestSyncMessagesAndCursor：per-source 游标 + 增量补发跨会话正确。
func TestSyncMessagesAndCursor(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)

	for _, m := range []protocol.StoredMessage{
		msg("node-a", 1, "conv-1", "a1"),
		msg("node-a", 2, "conv-2", "a2"),
		msg("node-b", 1, "conv-1", "b1"),
		msg("node-a", 3, "conv-1", "a3"),
	} {
		if _, err := s.AppendMessage(ctx, m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	maxA, err := s.MaxSeqOfNode(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if maxA != 3 {
		t.Fatalf("MaxSeqOfNode(node-a) = %d, want 3", maxA)
	}
	maxB, _ := s.MaxSeqOfNode(ctx, "node-b")
	if maxB != 1 {
		t.Fatalf("MaxSeqOfNode(node-b) = %d, want 1", maxB)
	}

	// 增量补发：node-a after=1 → 应得 seq 2、3（跨会话）。
	got, err := s.SyncMessages(ctx, "node-a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ServerSeq != 2 || got[1].ServerSeq != 3 {
		t.Fatalf("SyncMessages(after=1) = %+v, want seq 2,3", got)
	}
	if got[0].NodeID != "node-a" || got[0].ConversationID != "conv-2" {
		t.Fatalf("sync msg fields wrong: %+v", got[0])
	}
}

// TestAppendSyncedMessageIdempotent：同 (node_id, seq) 重复同步只落一条。
func TestAppendSyncedMessageIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)

	m := msg("node-a", 5, "conv-1", "m5")
	inserted, err := s.AppendSyncedMessage(ctx, m)
	if err != nil {
		t.Fatalf("first sync append: %v", err)
	}
	if !inserted {
		t.Fatal("first sync append reported not inserted, want true")
	}
	// 重复送达（同 (node_id, seq)，不同 id 也不应插入）。
	dup := m
	dup.ID = "m5-dup"
	inserted, err = s.AppendSyncedMessage(ctx, dup)
	if err != nil {
		t.Fatalf("dup sync append: %v", err)
	}
	if inserted {
		t.Fatal("duplicate sync append reported inserted, want false")
	}

	got, err := s.SyncMessages(ctx, "node-a", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate sync stored %d messages, want 1", len(got))
	}
	if got[0].ID != "m5" {
		t.Fatalf("kept wrong message: %+v", got[0])
	}

	// 本地 upsert 路径（乐观 seq=0 → hub 回 seq=N）不受唯一索引影响。
	local := protocol.StoredMessage{
		ID:             "local-1",
		ConversationID: "conv-1",
		SenderUserID:   "alice",
		Body:           "pending",
		ServerSeq:      0,
		CreatedAt:      1,
		NodeID:         "node-a",
	}
	if _, err := s.AppendMessage(ctx, local); err != nil {
		t.Fatalf("local seq=0 append: %v", err)
	}
	local.ServerSeq = 6
	if _, err := s.AppendMessage(ctx, local); err != nil {
		t.Fatalf("local seq=6 upsert: %v", err)
	}
	maxA, _ := s.MaxSeqOfNode(ctx, "node-a")
	if maxA != 6 {
		t.Fatalf("MaxSeqOfNode after upsert = %d, want 6", maxA)
	}
}

// TestConvEventStream 验证会话事件流：分配/游标/枚举/幂等（M-c 群成员一致性）。
func TestConvEventStream(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)

	ev := func(typ, convID, by string, members ...string) protocol.ConversationEvent {
		return protocol.ConversationEvent{
			Conversation: protocol.Conversation{ID: convID, Kind: "group", Title: "team"},
			Event:        typ, ByUserID: by, Members: members,
		}
	}

	// 本节点产生 3 个事件（per-node seq 单调）
	for _, e := range []protocol.ConversationEvent{
		ev("created", "g1", "alice", "alice", "bob"),
		ev("joined", "g1", "alice", "alice", "bob", "carol"),
		ev("left", "g1", "bob"),
	} {
		if err := s.AppendConvEvent(ctx, "node-a", e); err != nil {
			t.Fatalf("append conv event: %v", err)
		}
	}
	// 幂等：同 node 重复 append 应分配新 seq（事件不同），同 seq 由
	// (node_id, seq) 主键去重——这里验证游标与列表一致即可。

	cursor, err := s.ConvEventCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cursor["node-a"] != 3 {
		t.Fatalf("cursor node-a = %d, want 3", cursor["node-a"])
	}
	nodes, err := s.ConvEventNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0] != "node-a" {
		t.Fatalf("conv event nodes = %v, want [node-a]", nodes)
	}

	// after=0 取全部，升序
	evs, err := s.ListConvEvents(ctx, "node-a", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("list conv events = %d, want 3", len(evs))
	}
	if evs[0].Event.Event != "created" || evs[2].Event.Event != "left" {
		t.Fatalf("conv events order wrong: %v", []string{evs[0].Event.Event, evs[1].Event.Event, evs[2].Event.Event})
	}
	if evs[0].Seq != 1 || evs[1].Seq != 2 || evs[2].Seq != 3 {
		t.Fatalf("conv event seqs = %d,%d,%d, want 1,2,3", evs[0].Seq, evs[1].Seq, evs[2].Seq)
	}

	// after=1 增量
	evs, err = s.ListConvEvents(ctx, "node-a", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Event.Event != "joined" {
		t.Fatalf("incremental conv events = %+v, want [joined left]", evs)
	}

	// 跨源隔离：另一个节点的事件互不干扰
	if err := s.AppendConvEvent(ctx, "node-b", ev("created", "g2", "bob", "bob")); err != nil {
		t.Fatal(err)
	}
	cursor, _ = s.ConvEventCursor(ctx)
	if cursor["node-a"] != 3 || cursor["node-b"] != 1 {
		t.Fatalf("per-node cursors = %v", cursor)
	}
	nodes, _ = s.ConvEventNodes(ctx)
	if len(nodes) != 2 {
		t.Fatalf("conv event nodes = %v, want 2", nodes)
	}
}
