package libsql_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
)

// newTestStore 在 t.TempDir() 下开一个文件库（每个测试独立文件，自动清理）。
func newTestStore(t *testing.T) *libsql.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "hub-test.db")
	s, err := libsql.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open %q: %v", dsn, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_UserDeviceConversation_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := protocol.User{ID: "u1", Name: "Alice", AvatarSeed: "seed-1"}
	if err := s.SaveUser(ctx, u); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	got, err := s.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got != u {
		t.Errorf("user round-trip = %+v, want %+v", got, u)
	}

	d := protocol.Device{ID: "d1", UserID: "u1", Name: "web-aaa"}
	if err := s.SaveDevice(ctx, d); err != nil {
		t.Fatalf("SaveDevice: %v", err)
	}
	gotD, err := s.GetDevice(ctx, "d1")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if gotD != d {
		t.Errorf("device round-trip = %+v, want %+v", gotD, d)
	}

	c := protocol.Conversation{ID: "lobby", Kind: "channel", Title: "Lobby"}
	if err := s.SaveConversation(ctx, c); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	gotC, err := s.GetConversation(ctx, "lobby")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if gotC != c {
		t.Errorf("conversation round-trip = %+v, want %+v", gotC, c)
	}
}

func TestStore_GetMissing_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetUser(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetUser missing = %v, want core.ErrNotFound", err)
	}
	if _, err := s.GetDevice(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetDevice missing = %v, want core.ErrNotFound", err)
	}
	if _, err := s.GetConversation(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetConversation missing = %v, want core.ErrNotFound", err)
	}
}

func TestStore_UpsertUser(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.SaveUser(ctx, protocol.User{ID: "u1", Name: "Old"}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := s.SaveUser(ctx, protocol.User{ID: "u1", Name: "New", AvatarSeed: "s"}); err != nil {
		t.Fatalf("SaveUser upsert: %v", err)
	}
	got, err := s.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Name != "New" || got.AvatarSeed != "s" {
		t.Errorf("upsert = %+v, want Name=New AvatarSeed=s", got)
	}
}

func msg(seq uint64, id, conv string) protocol.StoredMessage {
	return protocol.StoredMessage{
		ID:             id,
		ClientNonce:    "nonce-" + id,
		ConversationID: conv,
		SenderUserID:   "alice",
		SenderDeviceID: "d1",
		Body:           "body-" + id,
		ServerSeq:      seq,
		CreatedAt:      1700000000000 + int64(seq),
	}
}

func TestStore_AppendAndHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i := uint64(1); i <= 5; i++ {
		if err := s.AppendMessage(ctx, msg(i, string(rune('a'+i-1)), "lobby")); err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	// 全量升序
	all, err := s.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("History len = %d, want 5", len(all))
	}
	for i, m := range all {
		if m.ServerSeq != uint64(i+1) {
			t.Errorf("all[%d].ServerSeq = %d, want %d (must be ascending)", i, m.ServerSeq, i+1)
		}
	}

	// after 过滤
	got, err := s.History(ctx, "lobby", 3, 0)
	if err != nil {
		t.Fatalf("History after=3: %v", err)
	}
	if len(got) != 2 || got[0].ServerSeq != 4 || got[1].ServerSeq != 5 {
		t.Errorf("History after=3 = %v, want seqs [4 5]", seqs(got))
	}

	// limit 截断
	got, err = s.History(ctx, "lobby", 0, 2)
	if err != nil {
		t.Fatalf("History limit=2: %v", err)
	}
	if len(got) != 2 || got[0].ServerSeq != 1 || got[1].ServerSeq != 2 {
		t.Errorf("History limit=2 = %v, want seqs [1 2]", seqs(got))
	}

	// 会话隔离
	other, err := s.History(ctx, "other", 0, 0)
	if err != nil {
		t.Fatalf("History other conv: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("History other conv = %v, want empty", seqs(other))
	}
}

func TestStore_AppendMessage_UpsertByID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 乐观写入：seq=0
	opt := msg(0, "local-1", "lobby")
	opt.Body = "pending"
	if err := s.AppendMessage(ctx, opt); err != nil {
		t.Fatalf("AppendMessage optimistic: %v", err)
	}
	// Hub 回环：同 ID 补齐 seq
	confirmed := msg(42, "local-1", "lobby")
	confirmed.Body = "confirmed"
	if err := s.AppendMessage(ctx, confirmed); err != nil {
		t.Fatalf("AppendMessage confirmed: %v", err)
	}

	all, err := s.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("after upsert len = %d, want 1 (same ID must not duplicate)", len(all))
	}
	if all[0].ServerSeq != 42 || all[0].Body != "confirmed" {
		t.Errorf("upserted = seq=%d body=%q, want seq=42 body=confirmed", all[0].ServerSeq, all[0].Body)
	}
}

func TestStore_AppendMessage_EmptyConv(t *testing.T) {
	// M12-A：空 conv = 大厅，是合法会话桶。
	s := newTestStore(t)
	m := msg(1, "x", "")
	if err := s.AppendMessage(t.Context(), m); err != nil {
		t.Fatalf("AppendMessage with empty convID (lobby): %v", err)
	}
	got, err := s.History(t.Context(), "", 0, 10)
	if err != nil {
		t.Fatalf("History lobby: %v", err)
	}
	if len(got) != 1 || got[0].Body != "body-x" {
		t.Fatalf("lobby history = %+v, want 1 msg", got)
	}
}

func TestStore_Cursors(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 未设置 = 0
	got, err := s.GetCursor(ctx, "d1", "lobby")
	if err != nil {
		t.Fatalf("GetCursor unset: %v", err)
	}
	if got != 0 {
		t.Errorf("unset cursor = %d, want 0", got)
	}

	if err := s.SetCursor(ctx, "d1", "lobby", 10); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	got, _ = s.GetCursor(ctx, "d1", "lobby")
	if got != 10 {
		t.Errorf("cursor = %d, want 10", got)
	}

	// 单调不回退
	if err := s.SetCursor(ctx, "d1", "lobby", 5); err != nil {
		t.Fatalf("SetCursor rollback: %v", err)
	}
	got, _ = s.GetCursor(ctx, "d1", "lobby")
	if got != 10 {
		t.Errorf("cursor after rollback = %d, want 10 (monotonic)", got)
	}

	// 设备 / 会话隔离
	got, _ = s.GetCursor(ctx, "d2", "lobby")
	if got != 0 {
		t.Errorf("d2 cursor = %d, want 0 (per-device)", got)
	}
}

func TestStore_MaxSeqAndRecentMessages(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 空库
	maxSeq, err := s.MaxSeq(ctx)
	if err != nil {
		t.Fatalf("MaxSeq empty: %v", err)
	}
	if maxSeq != 0 {
		t.Errorf("MaxSeq empty = %d, want 0", maxSeq)
	}

	for i := uint64(1); i <= 10; i++ {
		conv := "lobby"
		if i%2 == 0 {
			conv = "random"
		}
		if err := s.AppendMessage(ctx, msg(i, string(rune('a'+i-1)), conv)); err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	maxSeq, err = s.MaxSeq(ctx)
	if err != nil {
		t.Fatalf("MaxSeq: %v", err)
	}
	if maxSeq != 10 {
		t.Errorf("MaxSeq = %d, want 10", maxSeq)
	}

	// RecentMessages 跨会话、返回升序（DESC 取最近 N 条后反转）
	recent, err := s.RecentMessages(ctx, 3)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("RecentMessages len = %d, want 3", len(recent))
	}
	if seqs := seqs(recent); seqs[0] != 8 || seqs[1] != 9 || seqs[2] != 10 {
		t.Errorf("RecentMessages seqs = %v, want [8 9 10] ascending", seqs)
	}
}

// TestStore_PersistsAcrossReopen 是持久化的关键验收：关掉重开消息还在。
func TestStore_PersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub-persist.db")

	s, err := libsql.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s.AppendMessage(ctx, msg(7, "persist-1", "lobby")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.SetCursor(ctx, "d1", "lobby", 7); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := libsql.Open(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	all, err := s2.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("History after reopen: %v", err)
	}
	if len(all) != 1 || all[0].ServerSeq != 7 || all[0].Body != "body-persist-1" {
		t.Errorf("after reopen messages = %v", seqs(all))
	}
	maxSeq, _ := s2.MaxSeq(ctx)
	if maxSeq != 7 {
		t.Errorf("MaxSeq after reopen = %d, want 7", maxSeq)
	}
	cur, _ := s2.GetCursor(ctx, "d1", "lobby")
	if cur != 7 {
		t.Errorf("cursor after reopen = %d, want 7", cur)
	}
}

func seqs(ms []protocol.StoredMessage) []uint64 {
	out := make([]uint64, len(ms))
	for i, m := range ms {
		out[i] = m.ServerSeq
	}
	return out
}

func TestStore_FileMeta_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetFileMeta(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetFileMeta(missing) = %v, want ErrNotFound", err)
	}

	m := protocol.FileMeta{FileID: "f1", Name: "code.go", Size: 1234, Mime: "text/plain"}
	if err := s.SaveFileMeta(ctx, m); err != nil {
		t.Fatalf("SaveFileMeta: %v", err)
	}
	got, err := s.GetFileMeta(ctx, "f1")
	if err != nil {
		t.Fatalf("GetFileMeta: %v", err)
	}
	if got.FileID != "f1" || got.Name != "code.go" || got.Size != 1234 || got.Mime != "text/plain" || got.CreatedAt == 0 {
		t.Errorf("file meta round-trip = %+v", got)
	}

	// 幂等覆盖：同 ID 再存一次不报错，且元信息被更新。
	if err := s.SaveFileMeta(ctx, protocol.FileMeta{FileID: "f1", Name: "code-v2.go", Size: 99, Mime: "text/x-go"}); err != nil {
		t.Fatalf("SaveFileMeta overwrite: %v", err)
	}
	got2, _ := s.GetFileMeta(ctx, "f1")
	if got2.Name != "code-v2.go" || got2.Size != 99 {
		t.Errorf("file meta overwrite = %+v", got2)
	}

	if err := s.SaveFileMeta(ctx, protocol.FileMeta{FileID: ""}); err == nil {
		t.Fatal("SaveFileMeta(empty id) = nil, want error")
	}
}

func TestStore_MessageWithFileRef_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	m := protocol.StoredMessage{
		ID: "m1", ConversationID: "lobby", Body: "",
		ServerSeq: 1, CreatedAt: 1000,
		File: &protocol.FileRef{FileID: "f9", Name: "shot.png", Size: 8888, Mime: "image/png"},
	}
	if err := s.AppendMessage(ctx, m); err != nil {
		t.Fatalf("AppendMessage(with file): %v", err)
	}
	msgs, err := s.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	got := msgs[0]
	if got.File == nil {
		t.Fatal("File == nil after round-trip, want FileRef")
	}
	if *got.File != *m.File {
		t.Errorf("File = %+v, want %+v", *got.File, *m.File)
	}

	// 纯文本消息：File 保持 nil（老数据兼容）。
	if err := s.AppendMessage(ctx, protocol.StoredMessage{
		ID: "m2", ConversationID: "lobby", Body: "hello", ServerSeq: 2, CreatedAt: 1001,
	}); err != nil {
		t.Fatalf("AppendMessage(text): %v", err)
	}
	msgs2, _ := s.History(ctx, "lobby", 0, 0)
	if msgs2[1].File != nil {
		t.Errorf("text message File = %+v, want nil", msgs2[1].File)
	}
}

// TestStore_SearchMessages 验证 LIKE 通配符转义、降序排序与会话限定（v1.1）。
func TestStore_SearchMessages(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	msgs := []struct {
		id   string
		body string
		conv string
	}{
		{"m1", "hello world", "lobby"},
		{"m2", "hello alice 100% ready", "lobby"},
		{"m3", "under_score and 100% done", "g1"},
		{"m4", "HELLO UPPER", "g1"},
	}
	ids := []string{"m1", "m2", "m3", "m4"}
	for i, mm := range msgs {
		m := msg(uint64(i+1), ids[i], mm.conv)
		m.Body = mm.body
		if err := s.AppendMessage(ctx, m); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// 基本子串 + 大小写不敏感 + 降序
	hits, err := s.SearchMessages(ctx, "hello", "", 0)
	if err != nil {
		t.Fatalf("search hello: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hello hits = %d, want 3", len(hits))
	}
	// 降序：m4(HELLO) 在 m2、m1 之前
	if hits[0].ID != "m4" || hits[1].ID != "m2" || hits[2].ID != "m1" {
		t.Errorf("hello order = %s %s %s, want m4 m2 m1", hits[0].ID, hits[1].ID, hits[2].ID)
	}

	// % 转义：100% 只能命中字面含 100% 的两条，不会展开成通配符
	hits, err = s.SearchMessages(ctx, "100%", "", 0)
	if err != nil {
		t.Fatalf("search 100%%: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("100%% hits = %d, want 2 (literal match only)", len(hits))
	}

	// _ 转义：under_score 字面命中，不展开
	hits, err = s.SearchMessages(ctx, "under_score", "", 0)
	if err != nil {
		t.Fatalf("search under_score: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "m3" {
		t.Fatalf("under_score hits = %+v, want [m3]", hits)
	}

	// 会话限定
	hits, err = s.SearchMessages(ctx, "hello", "g1", 0)
	if err != nil {
		t.Fatalf("search conv g1: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "m4" {
		t.Fatalf("conv g1 hits = %+v, want [m4]", hits)
	}

	// 空 query / 无命中
	hits, err = s.SearchMessages(ctx, "   ", "", 0)
	if err != nil {
		t.Fatalf("search empty: %v", err)
	}
	if hits != nil {
		t.Fatalf("empty query hits = %+v, want nil", hits)
	}
	hits, err = s.SearchMessages(ctx, "zzz-no-such", "", 0)
	if err != nil {
		t.Fatalf("search miss: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("miss hits = %+v, want empty", hits)
	}
}

// TestStore_AppendMessage_ReplyToRoundTrip 验证 ReplyTo 往返（v1.1）。
func TestStore_AppendMessage_ReplyToRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	orig := msg(1, "hello", "lobby")
	orig.ReplyTo = &protocol.ReplyRef{ID: "m0", SenderUserID: "alice", Body: "被引用内容"}
	if err := s.AppendMessage(ctx, orig); err != nil {
		t.Fatalf("append: %v", err)
	}
	hist, err := s.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 || hist[0].ReplyTo == nil {
		t.Fatalf("ReplyTo lost: %+v", hist)
	}
	if hist[0].ReplyTo.ID != "m0" || hist[0].ReplyTo.SenderUserID != "alice" || hist[0].ReplyTo.Body != "被引用内容" {
		t.Fatalf("ReplyTo mismatch: %+v", hist[0].ReplyTo)
	}
}

// TestStore_ExportAll 验证 libsql 全量导出（v1.2）：各表数据往返完整。
func TestStore_ExportAll(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_ = s.SaveUser(ctx, protocol.User{ID: "u1", Name: "alice", AvatarSeed: "a"})
	_ = s.SaveDevice(ctx, protocol.Device{ID: "d1", UserID: "u1", Name: "laptop"})
	_ = s.SaveConversation(ctx, protocol.Conversation{ID: "g1", Kind: "group", Title: "team"})
	_ = s.SaveConversationMember(ctx, "g1", "u1")

	m := msg(1, "m1", "g1")
	m.ReplyTo = &protocol.ReplyRef{ID: "m0", SenderUserID: "bob", Body: "原消息"}
	m.File = &protocol.FileRef{FileID: "f1", Name: "x.png", Size: 3, Mime: "image/png"}
	_ = s.AppendMessage(ctx, m)
	_ = s.SetCursor(ctx, "d1", "g1", 1)
	_ = s.SaveFileMeta(ctx, protocol.FileMeta{FileID: "f1", Name: "x.png", Size: 3, Mime: "image/png", CreatedAt: 2})

	b, err := s.ExportAll(ctx)
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if b == nil || b.Schema != protocol.BackupSchema {
		t.Fatalf("schema = %+v", b)
	}
	if len(b.Users) != 1 || b.Users[0].Name != "alice" {
		t.Errorf("users = %+v", b.Users)
	}
	if len(b.Devices) != 1 || b.Devices[0].UserID != "u1" {
		t.Errorf("devices = %+v", b.Devices)
	}
	if len(b.Conversations) != 1 || len(b.Conversations[0].Members) != 1 {
		t.Errorf("conversations = %+v", b.Conversations)
	}
	if len(b.Messages) != 1 {
		t.Fatalf("messages = %d", len(b.Messages))
	}
	got := b.Messages[0]
	if got.ReplyTo == nil || got.ReplyTo.ID != "m0" {
		t.Errorf("ReplyTo 导出丢失: %+v", got.ReplyTo)
	}
	if got.File == nil || got.File.FileID != "f1" {
		t.Errorf("File 导出丢失: %+v", got.File)
	}
	if len(b.Cursors) != 1 || b.Cursors[0].ServerSeq != 1 {
		t.Errorf("cursors = %+v", b.Cursors)
	}
	if len(b.Files) != 1 || b.Files[0].FileID != "f1" {
		t.Errorf("files = %+v", b.Files)
	}

	// 空库：非 nil 的空切片 Backup（JSON 输出稳定）
	s2 := newTestStore(t)
	b2, err := s2.ExportAll(ctx)
	if err != nil {
		t.Fatalf("empty ExportAll: %v", err)
	}
	if b2 == nil {
		t.Fatalf("empty backup b2 is nil")
	}
	if b2.Users == nil {
		t.Fatalf("empty backup Users is nil (len %d)", len(b2.Users))
	}
	if b2.Messages == nil {
		t.Fatalf("empty backup Messages is nil (len %d)", len(b2.Messages))
	}
}
