package core

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
)

func TestVoiceCallUDPStreamingAndGossip(t *testing.T) {
	alice := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	defer alice.Close()
	bob := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	defer bob.Close()

	resA, err := alice.StartVoiceCall("", "")
	if err != nil {
		t.Fatalf("StartVoiceCall: %v", err)
	}
	aliceAddr := strings.TrimPrefix(resA, "call:calling:")

	received := make(chan []byte, 1)
	if err := bob.ensureLocalVoiceUDP("", ""); err != nil {
		t.Fatalf("bob ensureLocalVoiceUDP: %v", err)
	}
	bob.voice.mu.Lock()
	bob.voice.onFrame = func(_ uint32, _ int64, pcm []byte) {
		select {
		case received <- pcm:
		default:
		}
	}
	bob.voice.mu.Unlock()

	resB, err := bob.AcceptVoiceCall("", "", aliceAddr)
	if err != nil {
		t.Fatalf("AcceptVoiceCall: %v", err)
	}
	bobAddr := strings.TrimPrefix(resB, "call:active:")
	alice.setVoiceRemoteUDP(bobAddr)

	samplePCM := []byte{0x01, 0x02, 0x03, 0x04, 0xAA, 0xBB}
	if err := alice.SendVoiceFrame(samplePCM); err != nil {
		t.Fatalf("SendVoiceFrame: %v", err)
	}

	select {
	case got := <-received:
		if !bytes.Equal(got, samplePCM) {
			t.Fatalf("received PCM = %x, want %x", got, samplePCM)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UDP voice frame")
	}

	// 验证契约 v1.2 UDP 屏幕帧直传与 State 快照
	sampleScreen := "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQABAAD/"
	if err := alice.SendScreenFrame("bob", "", sampleScreen); err != nil {
		t.Fatalf("SendScreenFrame over UDP: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := bob.GetState(); st.ScreenShare != nil && st.ScreenShare.LatestFrame == sampleScreen {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := bob.GetState(); st.ScreenShare == nil || st.ScreenShare.LatestFrame != sampleScreen {
		t.Fatalf("bob ScreenShare state = %+v, want latestFrame=%q", st.ScreenShare, sampleScreen)
	}
	if st := alice.GetState(); st.VoiceCall == nil || st.VoiceCall.State != "active" {
		t.Fatalf("alice VoiceCall state = %+v, want active", st.VoiceCall)
	}

	_ = alice.StopVoiceCall("", "")
	if st := alice.VoiceCallStatus(); st != "idle" {
		t.Fatalf("VoiceCallStatus after stop = %q, want idle", st)
	}

	// 验证 /gossip 与 /screenshare 指令
	gRes, err := alice.SendText("", "/gossip add 100.93.183.47:7878,100.105.48.87:7878", "")
	if err != nil || !strings.Contains(gRes, "gossip:added:2") {
		t.Fatalf("unexpected gossip add result: %q, err=%v", gRes, err)
	}
	sRes, err := alice.SendText("", "/screenshare start 24", "")
	if err != nil || sRes != "screenshare:active:24fps" {
		t.Fatalf("unexpected screenshare start result: %q, err=%v", sRes, err)
	}

	// 验证二维码房间码导出与解析 (v2.4.0)
	rcRes, err := alice.SendText("", "/roomcode", "")
	if err != nil || !strings.HasPrefix(rcRes, "roomcode:lanchat://join?") {
		t.Fatalf("unexpected roomcode result: %q, err=%v", rcRes, err)
	}
	uri := strings.TrimPrefix(rcRes, "roomcode:")
	pAddr, _, pGossip, ok := ParseRoomInviteURI(uri)
	if !ok || pAddr == "" || len(pGossip) < 2 {
		t.Fatalf("ParseRoomInviteURI(%q) = addr:%q gossip:%v ok:%v", uri, pAddr, pGossip, ok)
	}

	// 验证 v3.0.0 多端加密历史漫游同步 (exportSince & mergeRemoteMessages)
	alice.history.append(appapi.ChatMessage{ID: "m-sync-1", From: "alice", To: "bob", Type: "text", Text: "第一条多端消息", TS: 1000})
	alice.history.append(appapi.ChatMessage{ID: "m-sync-2", From: "alice", To: "bob", Type: "text", Text: "第二条多端消息", TS: 2000})
	exported := alice.history.exportSince(0)
	if len(exported) != 2 {
		t.Fatalf("exportSince(0) len = %d, want 2", len(exported))
	}
	merged := bob.history.mergeRemoteMessages(exported)
	if merged != 2 {
		t.Fatalf("mergeRemoteMessages count = %d, want 2", merged)
	}
	if mergedAgain := bob.history.mergeRemoteMessages(exported); mergedAgain != 0 {
		t.Fatalf("idempotent mergeRemoteMessages count = %d, want 0", mergedAgain)
	}

	// 验证 v3.3.0 自动剪贴板开关与屏幕共享激光批注指令
	acRes, err := alice.SendText("", "/autoclip on", "")
	if err != nil || acRes != "autoclip:on" || !alice.IsAutoClipboard() {
		t.Fatalf("unexpected autoclip on: res=%q, enabled=%v, err=%v", acRes, alice.IsAutoClipboard(), err)
	}
	acOff, err := alice.SendText("", "/autoclip off", "")
	if err != nil || acOff != "autoclip:off" || alice.IsAutoClipboard() {
		t.Fatalf("unexpected autoclip off: res=%q, enabled=%v, err=%v", acOff, alice.IsAutoClipboard(), err)
	}
	anRes, err := alice.SendText("", "/annotate 0.42,0.75 请检查此函数", "")
	if err != nil || anRes != "annotate:0.42,0.75" {
		t.Fatalf("unexpected annotate result: %q, err=%v", anRes, err)
	}
}
