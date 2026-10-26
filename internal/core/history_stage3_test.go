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
