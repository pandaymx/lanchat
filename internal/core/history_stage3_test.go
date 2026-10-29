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

func TestHistoryInvertedIndexAndChronologicalMerge(t *testing.T) {
	dir := t.TempDir()
	h := newHistoryStore(dir, "")

	h.append(appapi.ChatMessage{
		ID:   "idx-2",
		From: "alice",
		To:   "bob",
		Type: "text",
		Text: "研发协同架构评审通过",
		TS:   2000,
	})
	if h.invertedIndexSize() == 0 {
		t.Fatal("expected inverted token index to be populated after append")
	}

	// 中文二元词组与英文子串检索
	hits := h.search("协同 评审", "bob")
	if len(hits) != 1 || hits[0].ID != "idx-2" {
		t.Fatalf("expected CJK bigram search hit idx-2, got %+v", hits)
	}

	// 乱序漫游合并（TS=1000 晚于 TS=2000 到达）应按 TS 升序排列并落盘 CAS 媒体
	diff := h.mergeRemoteMessagesWithDiff([]appapi.ChatMessage{
		{
			ID:   "idx-1",
			From: "bob",
			To:   "alice",
			Type: "text",
			Text: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
			TS:   1000,
		},
	})
	if len(diff) != 1 || diff[0].MediaMime != "image/png" || diff[0].MediaPath == "" {
		t.Fatalf("expected roamed image message to externalize CAS media, got %+v", diff)
	}
	snap := h.snapshot()
	if len(snap) != 2 || snap[0].ID != "idx-1" || snap[1].ID != "idx-2" {
		t.Fatalf("expected chronological sort [idx-1, idx-2], got %+v", snap)
	}

	// 撤回与物理销毁应同步清理倒排索引
	h.revoke("idx-2", "alice", 0)
	if hitsAfterRevoke := h.search("协同", "bob"); len(hitsAfterRevoke) != 0 {
		t.Fatalf("expected 0 hits after revoke, got %+v", hitsAfterRevoke)
	}
	h.purgeByID("idx-1")
	if len(h.snapshot()) != 1 {
		t.Fatalf("expected 1 message left after purgeByID, got %d", len(h.snapshot()))
	}
}

func TestAutoHistoryRoamingOnPeerJoinAndChannelSync(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	alice := New(Options{Nickname: "alice", OS: "linux", DownloadDir: t.TempDir(), Listener: la})
	t.Cleanup(alice.Close)
	if err := alice.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}

	// Alice 在 Bob 离线期间发送一条广播群消息，并创建公开频道发送一条频道消息
	if _, err := alice.SendText("", "offline broadcast notice", "*"); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.ChannelCreate("roam-dev", "auto roam test", false); err != nil {
		t.Fatal(err)
	}
	var chID string
	waitFor(t, time.Second, func() bool {
		chs := la.channelSnapshot()
		if len(chs) == 1 {
			chID = chs[0].ID
		}
		return chID != ""
	})
	if _, err := alice.SendText("", "offline channel message", chID); err != nil {
		t.Fatal(err)
	}

	// Bob 上线连接：无需手动点击 /sync，应自动增量漫游拉取离线期间的广播群消息
	lb := &recordingListener{}
	bob := New(Options{Nickname: "bob", OS: "linux", DownloadDir: t.TempDir(), Listener: lb})
	t.Cleanup(bob.Close)
	if err := bob.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		page := bob.GetHistoryPage("*", 0, 20)
		return len(page) == 1 && page[0].Text == "offline broadcast notice"
	})

	// Bob 加入频道前不应收到该频道历史；加入后自动漫游补齐离线期间的频道消息
	if err := bob.ChannelJoin(chID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		page := bob.GetHistoryPage(chID, 0, 20)
		return len(page) == 1 && page[0].Text == "offline channel message"
	})
}
