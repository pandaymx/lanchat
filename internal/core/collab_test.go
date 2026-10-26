package core

import (
	"bytes"
	"strings"
	"testing"
	"time"
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
}
