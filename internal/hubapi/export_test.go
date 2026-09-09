package hubapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pandaymx/lanchat/internal/hubapi"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// TestExportAPI_RoundTrip 验证 GET /api/export 返回完整 JSON 备份（v1.2）。
func TestExportAPI_RoundTrip(t *testing.T) {
	store := memory.New()
	// 灌入各表数据。
	_ = store.SaveUser(t.Context(), protocol.User{ID: "u1", Name: "alice", AvatarSeed: "a"})
	_ = store.SaveDevice(t.Context(), protocol.Device{ID: "d1", UserID: "u1", Name: "laptop"})
	_ = store.SaveConversation(t.Context(), protocol.Conversation{ID: "g1", Kind: "group", Title: "team"})
	_ = store.SaveConversationMember(t.Context(), "g1", "u1")
	_ = store.AppendMessage(t.Context(), protocol.StoredMessage{
		ID: "m1", ConversationID: "g1", SenderUserID: "u1", Body: "hi", ServerSeq: 1, CreatedAt: 1,
	})
	_ = store.SetCursor(t.Context(), "d1", "g1", 1)
	_ = store.SaveFileMeta(t.Context(), protocol.FileMeta{FileID: "f1", Name: "x.png", Size: 3, Mime: "image/png", CreatedAt: 2})

	mux := http.NewServeMux()
	hubapi.NewExportAPI(store).Routes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatalf("GET /api/export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Fatal("缺少 Content-Disposition 下载头")
	}

	var b protocol.Backup
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	if b.Schema != protocol.BackupSchema {
		t.Fatalf("schema = %q, want %q", b.Schema, protocol.BackupSchema)
	}
	if b.ExportedAt == 0 {
		t.Fatal("ExportedAt 未设置")
	}
	if len(b.Users) != 1 || b.Users[0].ID != "u1" {
		t.Fatalf("users = %+v", b.Users)
	}
	if len(b.Devices) != 1 || b.Devices[0].ID != "d1" {
		t.Fatalf("devices = %+v", b.Devices)
	}
	if len(b.Conversations) != 1 || len(b.Conversations[0].Members) != 1 {
		t.Fatalf("conversations = %+v", b.Conversations)
	}
	if len(b.Messages) != 1 || b.Messages[0].Body != "hi" {
		t.Fatalf("messages = %+v", b.Messages)
	}
	if len(b.Cursors) != 1 || b.Cursors[0].ServerSeq != 1 {
		t.Fatalf("cursors = %+v", b.Cursors)
	}
	if len(b.Files) != 1 || b.Files[0].FileID != "f1" {
		t.Fatalf("files = %+v", b.Files)
	}
}

// TestExportAPI_MethodNotAllowed 验证非 GET 返回 405。
func TestExportAPI_MethodNotAllowed(t *testing.T) {
	store := memory.New()
	mux := http.NewServeMux()
	hubapi.NewExportAPI(store).Routes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/export", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
