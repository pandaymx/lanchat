package libsql

import (
	"context"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestE2EKeyring：保存/查询 + 消息自我声明自动提取（AppendMessage）。
func TestE2EKeyring(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 显式保存与查询。
	if err := st.SaveE2EKey(ctx, "dev-a", "pubkey-A"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetE2EKey(ctx, "dev-a")
	if err != nil || got != "pubkey-A" {
		t.Fatalf("get = %q, %v", got, err)
	}
	// 未记录返回空串。
	missing, _ := st.GetE2EKey(ctx, "dev-unknown")
	if missing != "" {
		t.Fatalf("missing = %q, want empty", missing)
	}
	// 覆盖更新。
	if err := st.SaveE2EKey(ctx, "dev-a", "pubkey-A2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetE2EKey(ctx, "dev-a"); got != "pubkey-A2" {
		t.Fatalf("after update = %q", got)
	}

	// 消息自我声明提取：AppendMessage 落库时自动进 keyring。
	if _, err := st.AppendMessage(ctx, protocol.StoredMessage{
		ID: "m1", ConversationID: "c1", SenderDeviceID: "dev-b",
		E2EKey: "pubkey-B", Body: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetE2EKey(ctx, "dev-b"); got != "pubkey-B" {
		t.Fatalf("extract from message = %q, want pubkey-B", got)
	}
	// 无 E2EKey 的消息不写。
	if _, err := st.AppendMessage(ctx, protocol.StoredMessage{
		ID: "m2", ConversationID: "c1", SenderDeviceID: "dev-c", Body: "plain",
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetE2EKey(ctx, "dev-c"); got != "" {
		t.Fatalf("plain message must not write keyring, got %q", got)
	}

	// 吊销：删除后查询为空；重复删除幂等。
	if err := st.DeleteE2EKey(ctx, "dev-a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetE2EKey(ctx, "dev-a"); got != "" {
		t.Fatalf("after delete = %q, want empty", got)
	}
	if err := st.DeleteE2EKey(ctx, "dev-a"); err != nil {
		t.Fatalf("re-delete should be idempotent: %v", err)
	}
}

// TestE2EKeyringSyncExtract：mesh 同步消息也自动提取。
func TestE2EKeyringSyncExtract(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ok, err := st.AppendSyncedMessage(ctx, protocol.StoredMessage{
		ID: "s1", ConversationID: "c1", NodeID: "n1", ServerSeq: 1,
		SenderDeviceID: "dev-x", E2EKey: "pubkey-X", Body: "synced",
	})
	if err != nil || !ok {
		t.Fatalf("append synced: %v, %v", ok, err)
	}
	if got, _ := st.GetE2EKey(ctx, "dev-x"); got != "pubkey-X" {
		t.Fatalf("sync extract = %q", got)
	}
}
