package core

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
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

	// 验证 v3.4.0 多维本地加密全文检索与 P2P 文件夹增量去重同步
	alice.history.append(appapi.ChatMessage{
		ID:     "m-code-1",
		From:   "alice",
		To:     "bob",
		Type:   "text",
		Text:   "```go\nfunc main() {}\n```",
		Pinned: true,
		TS:     3000,
	})
	codeHits := alice.SearchHistory("from:alice type:code main", "")
	if len(codeHits) != 1 || codeHits[0].ID != "m-code-1" {
		t.Fatalf("SearchHistory(from:alice type:code main) = %+v, want m-code-1", codeHits)
	}
	pinHits := alice.SearchHistory("type:pin since:2500", "")
	if len(pinHits) != 1 || pinHits[0].ID != "m-code-1" {
		t.Fatalf("SearchHistory(type:pin since:2500) = %+v, want m-code-1", pinHits)
	}

	syncDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(syncDir, "doc1.txt"), []byte("hello-v1"), 0o600)
	_ = os.WriteFile(filepath.Join(syncDir, "doc2.txt"), []byte("world-v1"), 0o600)
	fs1, err := alice.SendText("", "/foldersync "+syncDir, "")
	if err != nil || fs1 != "foldersync:queued:2" {
		t.Fatalf("first /foldersync = %q, err=%v, want foldersync:queued:2", fs1, err)
	}
	fs2, err := alice.SendText("", "/foldersync "+syncDir, "")
	if err != nil || fs2 != "foldersync:queued:0" {
		t.Fatalf("second unchanged /foldersync = %q, err=%v, want foldersync:queued:0", fs2, err)
	}
	_ = os.WriteFile(filepath.Join(syncDir, "doc2.txt"), []byte("world-v2-modified"), 0o600)
	fs3, err := alice.SendText("", "/foldersync "+syncDir, "")
	if err != nil || fs3 != "foldersync:queued:1" {
		t.Fatalf("third modified /foldersync = %q, err=%v, want foldersync:queued:1", fs3, err)
	}

	// 验证 v4.0.0 Ed25519 零信任节点准入证书签发、验签与防篡改 + /netdiag 诊断雷达
	tok, rootPub, err := alice.IssueNodeCertificate("dev-bob-01", "QXJ-SecOps", 3600)
	if err != nil || tok == "" || rootPub == "" {
		t.Fatalf("IssueNodeCertificate failed: tok=%q pub=%q err=%v", tok, rootPub, err)
	}
	sub, org, ok := VerifyNodeCertificate(tok, rootPub)
	if !ok || sub != "dev-bob-01" || org != "QXJ-SecOps" {
		t.Fatalf("VerifyNodeCertificate = (%q, %q, %v), want (dev-bob-01, QXJ-SecOps, true)", sub, org, ok)
	}
	if _, _, okTamper := VerifyNodeCertificate(tok+"tampered", rootPub); okTamper {
		t.Fatal("VerifyNodeCertificate should reject tampered signature")
	}
	vCmd, err := bob.SendText("", "/trust verify "+tok+" "+rootPub, "")
	if err != nil || vCmd != "trust:verified:dev-bob-01@QXJ-SecOps" {
		t.Fatalf("/trust verify = %q, err=%v", vCmd, err)
	}
	diagRes, err := alice.SendText("", "/netdiag", "")
	if err != nil || !strings.HasPrefix(diagRes, "netdiag:conn=") {
		t.Fatalf("/netdiag = %q, err=%v", diagRes, err)
	}
}

func TestScreenCapturePipelineAndAutoClipboardDaemon(t *testing.T) {
	alice := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	defer alice.Close()
	bob := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	defer bob.Close()

	// 1. 建立 Alice <-> Bob UDP 通道并验证 StartScreenShare 后台抓屏推流管线自动推送 LCTPSCREEN 帧
	resA, err := alice.StartVoiceCall("bob", "")
	if err != nil {
		t.Fatalf("StartVoiceCall: %v", err)
	}
	aliceUDP := strings.TrimPrefix(resA, "call:calling:")
	resB, err := bob.AcceptVoiceCall("alice", "", aliceUDP)
	if err != nil {
		t.Fatalf("AcceptVoiceCall: %v", err)
	}
	bobUDP := strings.TrimPrefix(resB, "call:active:")
	alice.setVoiceRemoteUDP(bobUDP)

	collabProviderMu.Lock()
	screenCaptureProvider = func(seq int) ([]byte, error) {
		return renderSyntheticScreenJPEG(seq), nil
	}
	collabProviderMu.Unlock()
	defer func() {
		collabProviderMu.Lock()
		screenCaptureProvider = nil
		collabProviderMu.Unlock()
	}()

	if _, err := alice.StartScreenShare("bob", "", 20); err != nil {
		t.Fatalf("StartScreenShare: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var receivedFrame string
	for time.Now().Before(deadline) {
		if st := bob.GetState(); st.ScreenShare != nil && strings.HasPrefix(st.ScreenShare.LatestFrame, "data:image/jpeg;base64,") {
			receivedFrame = st.ScreenShare.LatestFrame
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if receivedFrame == "" {
		t.Fatal("timed out waiting for automatic screen capture frame over UDP")
	}
	if err := alice.StopScreenShare("bob", ""); err != nil {
		t.Fatalf("StopScreenShare: %v", err)
	}
	if st := alice.GetState(); st.ScreenShare != nil && st.ScreenShare.Active {
		t.Fatalf("expected alice ScreenShare inactive after StopScreenShare, got %+v", st.ScreenShare)
	}

	// 2. 验证后台剪贴板双向自动同步守护协程（SHA-256 哈希去重 + 远端自动写入系统剪贴板）
	var (
		clipMu     = make(chan string, 4)
		mockSource = "LANChat-AutoClip-Payload-v4.8.0"
	)
	collabProviderMu.Lock()
	clipboardReadProvider = func() (string, error) {
		return mockSource, nil
	}
	clipboardWriteProvider = func(text string) error {
		select {
		case clipMu <- text:
		default:
		}
		return nil
	}
	collabProviderMu.Unlock()
	defer func() {
		collabProviderMu.Lock()
		clipboardReadProvider = nil
		clipboardWriteProvider = nil
		collabProviderMu.Unlock()
	}()

	alice.SetAutoClipboardTarget(true, "bob", "")
	defer alice.SetAutoClipboard(false)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if alice.LastClipboard() == mockSource {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := alice.LastClipboard(); got != mockSource {
		t.Fatalf("alice LastClipboard() = %q, want %q", got, mockSource)
	}

	// 模拟 Bob 收到 sys:clip: 控制帧，应自动写入本机系统剪贴板并更新哈希防回环
	bob.handleInboundControlText(&protocol.Envelope{ID: "clip-1", From: "alice"}, "sys:clip:"+mockSource)
	select {
	case written := <-clipMu:
		if written != mockSource {
			t.Fatalf("clipboardWriteProvider wrote %q, want %q", written, mockSource)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected inbound sys:clip: to write to system clipboard")
	}
}

func TestIncomingCallRingingBannerAndDesktopNotifications(t *testing.T) {
	alice := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	defer alice.Close()
	bob := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	defer bob.Close()

	type notifEvent struct {
		title  string
		body   string
		urgent bool
	}
	notifCh := make(chan notifEvent, 8)
	collabProviderMu.Lock()
	desktopNotifyProvider = func(title, body string, urgent bool) {
		select {
		case notifCh <- notifEvent{title: title, body: body, urgent: urgent}:
		default:
		}
	}
	collabProviderMu.Unlock()
	defer func() {
		collabProviderMu.Lock()
		desktopNotifyProvider = nil
		collabProviderMu.Unlock()
	}()

	resA, err := alice.StartVoiceCall("bob", "")
	if err != nil {
		t.Fatalf("alice StartVoiceCall: %v", err)
	}
	aliceUDP := strings.TrimPrefix(resA, "call:calling:")

	// 模拟 Bob 收到 Alice 的 sys:call:offer:<aliceUDP>，应进入 ringing 状态并触发紧急来电桌面通知
	bob.handleInboundControlText(&protocol.Envelope{ID: "call-offer-1", From: "alice"}, "sys:call:offer:"+aliceUDP)

	stBob := bob.GetState()
	if stBob.VoiceCall == nil || stBob.VoiceCall.State != "ringing" || stBob.VoiceCall.PeerID != "alice" {
		t.Fatalf("bob VoiceCall state after offer = %+v, want state=ringing peerId=alice", stBob.VoiceCall)
	}
	select {
	case ev := <-notifCh:
		if !ev.urgent || !strings.Contains(ev.title, "语音来电") {
			t.Fatalf("unexpected desktop notification for incoming call: %+v", ev)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected urgent desktop notification on incoming voice call")
	}

	// Bob 点击横幅「🟢 接听」(/call accept)，自动使用缓存的 remoteUDP 建立双向音频流并切换为 active
	accRes, err := bob.SendText("alice", "/call accept", "")
	if err != nil || !strings.HasPrefix(accRes, "call:active:") {
		t.Fatalf("bob /call accept = %q, err=%v, want call:active:...", accRes, err)
	}
	if st := bob.GetState(); st.VoiceCall == nil || st.VoiceCall.State != "active" {
		t.Fatalf("bob VoiceCall state after accept = %+v, want active", st.VoiceCall)
	}

	// 验证免打扰 (/dnd on) 抑制桌面通知
	bob.SetDND(true)
	bob.notifyDesktopEvent("不应弹出的通知", "DND 模式下静音", false)
	select {
	case ev := <-notifCh:
		t.Fatalf("expected no notification when DND is enabled, got %+v", ev)
	default:
	}
}

func TestVoiceJitterBufferOutOfOrderAndCrossPlatformAudioCandidates(t *testing.T) {
	// 1. 验证 Linux / macOS / Windows 16kHz 16-bit 单声道采集与播放管线候选命令完备性
	for _, goos := range []string{"linux", "darwin", "windows"} {
		capCands := rawAudioCaptureCandidatesForOS(goos)
		playCands := rawAudioPlayerCandidatesForOS(goos)
		if len(capCands) < 2 {
			t.Fatalf("expected >=2 audio capture candidates for %s, got %d", goos, len(capCands))
		}
		if len(playCands) < 2 {
			t.Fatalf("expected >=2 audio player candidates for %s, got %d", goos, len(playCands))
		}
	}

	// 2. 验证 40ms 自适应 Jitter Buffer 乱序包重排、重复包去重与过期包丢弃
	type deliveredFrame struct {
		seq uint32
		ts  int64
		pcm string
	}
	outCh := make(chan deliveredFrame, 16)
	jb := newVoiceJitterBuffer(35*time.Millisecond, 8, func(seq uint32, ts int64, pcm []byte) {
		outCh <- deliveredFrame{seq: seq, ts: ts, pcm: string(pcm)}
	})
	defer jb.Close()

	// 模拟乱序到达：seq=4, seq=2, seq=1, seq=2(重复), seq=3
	jb.Push(4, 1060, []byte("frame-4"))
	jb.Push(2, 1020, []byte("frame-2"))
	jb.Push(1, 1000, []byte("frame-1"))
	jb.Push(2, 1020, []byte("frame-2-dup"))
	jb.Push(3, 1040, []byte("frame-3"))

	wantSeqs := []uint32{1, 2, 3, 4}
	wantPayloads := []string{"frame-1", "frame-2", "frame-3", "frame-4"}
	for i, wantSeq := range wantSeqs {
		select {
		case got := <-outCh:
			if got.seq != wantSeq || got.pcm != wantPayloads[i] {
				t.Fatalf("jitter buffer item[%d] = (seq=%d, pcm=%q), want (seq=%d, pcm=%q)",
					i, got.seq, got.pcm, wantSeq, wantPayloads[i])
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for jitter buffer item[%d] (want seq=%d)", i, wantSeq)
		}
	}

	// 已播放序列号之前的迟到旧包（seq=2）应被丢弃
	jb.Push(2, 1020, []byte("frame-2-stale"))
	jb.Flush()
	select {
	case unexpected := <-outCh:
		t.Fatalf("expected stale seq=2 to be dropped, got %+v", unexpected)
	default:
	}

	// 3. 验证通话静音（Mute）切换
	alice := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	defer alice.Close()
	if _, err := alice.StartVoiceCall("bob", ""); err != nil {
		t.Fatalf("StartVoiceCall: %v", err)
	}
	resMute, err := alice.SendText("bob", "/call mute", "")
	if err != nil || resMute != "call:muted" || !alice.IsVoiceMuted() {
		t.Fatalf("/call mute = %q, err=%v, muted=%v", resMute, err, alice.IsVoiceMuted())
	}
	// 静音状态下调用 SendVoiceFrame 应静默丢弃且不递增 framesOut
	if err := alice.SendVoiceFrame([]byte{0x11, 0x22}); err != nil {
		t.Fatalf("SendVoiceFrame while muted returned err: %v", err)
	}
	if st := alice.GetState(); st.VoiceCall != nil && st.VoiceCall.FramesOut != 0 {
		t.Fatalf("expected 0 framesOut while muted, got %d", st.VoiceCall.FramesOut)
	}
	resUnmute, err := alice.SendText("bob", "/call unmute", "")
	if err != nil || resUnmute != "call:unmuted" || alice.IsVoiceMuted() {
		t.Fatalf("/call unmute = %q, err=%v, muted=%v", resUnmute, err, alice.IsVoiceMuted())
	}
}

func TestVoiceAndScreenUDPBlockedAutoFallbackToSignalingRelay(t *testing.T) {
	url := startServer(t)
	alice := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	t.Cleanup(alice.Close)
	bob := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	t.Cleanup(bob.Close)

	if err := alice.Connect(url, testPSK); err != nil {
		t.Fatalf("alice Connect: %v", err)
	}
	if err := bob.Connect(url, testPSK); err != nil {
		t.Fatalf("bob Connect: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return len(alice.GetState().Peers) == 1 })
	waitFor(t, 2*time.Second, func() bool { return len(bob.GetState().Peers) == 1 })

	bobID := bob.selfIDValue()
	aliceID := alice.selfIDValue()

	// Bob 准备接收语音帧（经由 Jitter Buffer）
	receivedPCM := make(chan []byte, 4)
	if err := bob.ensureLocalVoiceUDP(aliceID, ""); err != nil {
		t.Fatalf("bob ensureLocalVoiceUDP: %v", err)
	}
	bob.voice.mu.Lock()
	bob.voice.onFrame = func(_ uint32, _ int64, pcm []byte) {
		select {
		case receivedPCM <- pcm:
		default:
		}
	}
	bob.voice.mu.Unlock()

	// Alice 发起呼叫，但将远端 UDP 指向不可达黑洞端口，并模拟已连续 2 秒未收到任何 UDP 入站包
	if _, err := alice.StartVoiceCall(bobID, ""); err != nil {
		t.Fatalf("alice StartVoiceCall: %v", err)
	}
	// 分配一个立即关闭的本地 UDP 端口充当防火墙阻断的黑洞地址
	blackholePC, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket blackhole: %v", err)
	}
	blackholeAddr := blackholePC.LocalAddr().String()
	_ = blackholePC.Close()

	alice.setVoiceRemoteUDP(blackholeAddr)
	alice.voice.activeSinceNano.Store(time.Now().Add(-2500 * time.Millisecond).UnixNano())

	// 1. Alice 发送语音帧：检测到连续 >2s 未收到 UDP 入站包，自动回退至控制面加密信令通道转发
	wantPCM := []byte("fallback-pcm-over-signaling-v5.3.0")
	if err := alice.SendVoiceFrame(wantPCM); err != nil {
		t.Fatalf("alice SendVoiceFrame fallback: %v", err)
	}
	if !strings.Contains(alice.VoiceCallStatus(), "mode=relay") {
		t.Fatalf("expected alice VoiceCallStatus to report mode=relay, got %q", alice.VoiceCallStatus())
	}

	select {
	case got := <-receivedPCM:
		if !bytes.Equal(got, wantPCM) {
			t.Fatalf("bob received fallback PCM = %q, want %q", got, wantPCM)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for voice frame over signaling relay fallback")
	}

	// 2. Alice 发送投屏帧：同样自动跳过黑洞 UDP 通道并经由加密信令送达 Bob
	wantScreen := "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEASABIAAD/relay-fallback"
	if err := alice.SendScreenFrame(bobID, "", wantScreen); err != nil {
		t.Fatalf("alice SendScreenFrame fallback: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := bob.GetState(); st.ScreenShare != nil && st.ScreenShare.LatestFrame == wantScreen {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := bob.GetState(); st.ScreenShare == nil || st.ScreenShare.LatestFrame != wantScreen {
		t.Fatalf("bob ScreenShare after UDP fallback = %+v, want %q", st.ScreenShare, wantScreen)
	}

	// 3. 当对端真实 UDP 包恢复到达时，自动解除降级并切回 mode=udp
	alice.voice.recordUDPInboundNow()
	if !strings.Contains(alice.VoiceCallStatus(), "mode=udp") {
		t.Fatalf("expected alice VoiceCallStatus to recover to mode=udp after inbound UDP, got %q", alice.VoiceCallStatus())
	}
}
