package core

import (
	"os"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
)

func TestHistoryCompactionAndPagination(t *testing.T) {
	dir := t.TempDir()
	h := newHistoryStore(dir, "test-key")

	now := time.Now().UnixMilli()
	for i := 0; i < 5; i++ {
		h.append(appapi.ChatMessage{
			ID:   "m" + string(rune('1'+i)),
			From: "alice",
			To:   "bob",
			Type: "text",
			Text: "hello",
			TS:   now + int64(i*1000),
		})
	}
	h.reactBy("m1", "bob", "👍")
	h.edit("m1", "alice", "hello edited")
	h.pin("m1")
	h.markRead("m1", "bob")

	removed := h.compact()
	if removed != 4 {
		t.Fatalf("compact removed %d redundant lines, want 4", removed)
	}

	page1 := h.page("bob", 0, 2)
	if len(page1) != 2 || page1[0].ID != "m4" || page1[1].ID != "m5" {
		t.Fatalf("unexpected page1: %+v", page1)
	}
	page2 := h.page("bob", page1[0].TS, 2)
	if len(page2) != 2 || page2[0].ID != "m2" || page2[1].ID != "m3" {
		t.Fatalf("unexpected page2: %+v", page2)
	}
}

func TestMediaCASExternalization(t *testing.T) {
	dir := t.TempDir()
	h := newHistoryStore(dir, "")

	h.append(appapi.ChatMessage{
		ID:   "voice-1",
		From: "alice",
		To:   "bob",
		Type: "text",
		Text: "🎤 [语音消息 2\"] data:audio/mp4;base64,AAAAIGZ0eXBpc29t",
		TS:   time.Now().UnixMilli(),
	})

	msg, ok := h.findByID("voice-1")
	if !ok {
		t.Fatal("voice-1 not found")
	}
	if msg.MediaMime != "audio/mp4" {
		t.Fatalf("MediaMime = %q, want audio/mp4", msg.MediaMime)
	}
	if msg.MediaPath == "" {
		t.Fatal("expected MediaPath to be populated in CAS directory")
	}
	if _, err := os.Stat(msg.MediaPath); err != nil {
		t.Fatalf("expected CAS file at %s: %v", msg.MediaPath, err)
	}
}

func TestHistoryPersistenceAcrossRestartsAndPickDownloadDir(t *testing.T) {
	dir := t.TempDir()

	// 1. 启动时未传 DownloadDir，先在内存写入消息，再调用 PickDownloadDir 应自动落盘。
	c1 := New(Options{Nickname: "Alice", OS: "linux"})
	defer c1.Close()
	if c1.GetState().SelfID == "" {
		t.Fatal("expected GetState().SelfID to be non-empty before Connect")
	}
	if err := c1.PickDownloadDir(dir); err != nil {
		t.Fatalf("PickDownloadDir: %v", err)
	}
	selfID1 := c1.GetState().SelfID
	c1.history.append(appapi.ChatMessage{
		ID:   "persist-1",
		From: selfID1,
		To:   "dev-peer",
		Type: "text",
		Text: "persisted across sessions",
		TS:   time.Now().UnixMilli(),
	})

	// 2. 模拟重启客户端（尚未 Connect 时调用 GetState），应完整恢复 SelfID 与加密历史消息。
	c2 := New(Options{Nickname: "Alice", OS: "linux", DownloadDir: dir})
	defer c2.Close()
	st2 := c2.GetState()
	if st2.SelfID != selfID1 {
		t.Fatalf("restarted SelfID = %q, want %q", st2.SelfID, selfID1)
	}
	if len(st2.Messages) != 1 || st2.Messages[0].ID != "persist-1" || st2.Messages[0].Text != "persisted across sessions" {
		t.Fatalf("restarted Messages = %+v, want [persist-1]", st2.Messages)
	}
}
