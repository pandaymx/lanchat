package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/server"
)

const testPSK = "test-psk"

// startServer 在随机端口启动测试服务器，返回 WS URL。
func startServer(t *testing.T) string {
	t.Helper()
	hash, err := config.HashPSK(testPSK)
	if err != nil {
		t.Fatal(err)
	}
	opts := server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           hash,
		HeartbeatInterval: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ShutdownGrace:     time.Second,
	}
	srv, err := server.New(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
		}
	})
	select {
	case <-srv.Ready():
	case <-time.After(time.Second):
		t.Fatal("server not ready")
	}
	return "ws://" + srv.ListenAddr() + opts.Path
}

// startServerWithRelay 启动同时承载中继数据面的测试服务器，返回 WS URL。
func startServerWithRelay(t *testing.T) string {
	t.Helper()
	hash, err := config.HashPSK(testPSK)
	if err != nil {
		t.Fatal(err)
	}
	opts := server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           hash,
		HeartbeatInterval: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ShutdownGrace:     time.Second,
		RelayListen:       "127.0.0.1:0",
		RelayHost:         "127.0.0.1",
	}
	srv, err := server.New(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
		}
	})
	select {
	case <-srv.Ready():
	case <-time.After(time.Second):
		t.Fatal("server not ready")
	}
	return "ws://" + srv.ListenAddr() + opts.Path
}

// receivedMsg 记录入站消息的路由信息（from/group）用于群组断言。
type receivedMsg struct {
	from, group, typ, text string
}

// recordingListener 记录全部事件供断言。
type recordingListener struct {
	mu       sync.Mutex
	conn     []appapi.ConnState
	joined   []appapi.Peer
	left     []string
	messages []string
	recvd    []receivedMsg
	progress []appapi.Transfer
	done     []string
	failed   []string
	channels []appapi.Channel
}

func (l *recordingListener) OnConnChanged(s appapi.ConnState, _ string) {
	l.mu.Lock()
	l.conn = append(l.conn, s)
	l.mu.Unlock()
}

func (l *recordingListener) OnPeerJoined(p appapi.Peer) {
	l.mu.Lock()
	l.joined = append(l.joined, p)
	l.mu.Unlock()
}

func (l *recordingListener) OnPeerLeft(id string) {
	l.mu.Lock()
	l.left = append(l.left, id)
	l.mu.Unlock()
}

func (l *recordingListener) OnMessageReceived(from, group, _, typ, text string) {
	l.mu.Lock()
	l.messages = append(l.messages, text)
	l.recvd = append(l.recvd, receivedMsg{from: from, group: group, typ: typ, text: text})
	l.mu.Unlock()
}

func (l *recordingListener) OnTransferProgress(t appapi.Transfer) {
	l.mu.Lock()
	l.progress = append(l.progress, t)
	l.mu.Unlock()
}

func (l *recordingListener) OnTransferDone(id string) {
	l.mu.Lock()
	l.done = append(l.done, id)
	l.mu.Unlock()
}

func (l *recordingListener) OnTransferFailed(id, _ string) {
	l.mu.Lock()
	l.failed = append(l.failed, id)
	l.mu.Unlock()
}

func (l *recordingListener) OnGroupMatrix(string, string, []byte) {}

func (l *recordingListener) OnChannelUpdated(channels []appapi.Channel) {
	l.mu.Lock()
	l.channels = append([]appapi.Channel(nil), channels...)
	l.mu.Unlock()
}

func (l *recordingListener) snapshot() (conns []appapi.ConnState, msgs, done, failed []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]appapi.ConnState(nil), l.conn...),
		append([]string(nil), l.messages...),
		append([]string(nil), l.done...),
		append([]string(nil), l.failed...)
}

// channelSnapshot 返回最近一次 OnChannelUpdated 的频道列表拷贝。
func (l *recordingListener) channelSnapshot() []appapi.Channel {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]appapi.Channel(nil), l.channels...)
}

// progressSnapshot 返回全部任务进度事件的拷贝。
func (l *recordingListener) progressSnapshot() []appapi.Transfer {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]appapi.Transfer(nil), l.progress...)
}

// recvdSnapshot 返回收到消息的路由记录拷贝。
func (l *recordingListener) recvdSnapshot() []receivedMsg {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]receivedMsg(nil), l.recvd...)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within " + timeout.String())
}

// TestConnectHandshake 验证连接、握手与初始状态。
func TestConnectHandshake(t *testing.T) {
	url := startServer(t)
	l := &recordingListener{}
	c := New(Options{Nickname: "alice", OS: "linux", Listener: l})
	t.Cleanup(c.Close)

	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	st := c.GetState()
	if st.Conn != appapi.ConnConnected {
		t.Fatalf("conn = %s", st.Conn)
	}
	// 初始连接成功必须发出 connected 事件，UI 依赖它切换界面。
	conns, _, _, _ := l.snapshot()
	hasConnected := false
	for _, s := range conns {
		if s == appapi.ConnConnected {
			hasConnected = true
		}
	}
	if !hasConnected {
		t.Fatalf("listener 未收到 connected 事件，实际 %v", conns)
	}
	if st.SelfID == "" {
		t.Fatal("empty self id")
	}
	if st.Nickname != "alice" {
		t.Fatalf("nickname = %s", st.Nickname)
	}

	// 错误口令必须被拒。
	bad := New(Options{Nickname: "mallory"})
	t.Cleanup(bad.Close)
	if err := bad.Connect(url, "wrong"); err == nil {
		t.Fatal("expected auth failure")
	}
	if bad.GetState().Conn != appapi.ConnAuthFailed {
		t.Fatalf("bad client conn = %s", bad.GetState().Conn)
	}
}

// TestConnectRejectsDoubleConnect 连接态下再次 Connect 应报错。
func TestConnectRejectsDoubleConnect(t *testing.T) {
	url := startServer(t)
	c := New(Options{})
	t.Cleanup(c.Close)
	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(url, testPSK); err == nil {
		t.Fatal("expected error on second Connect")
	}
}

// TestPeerRoster 两客户端互见，对端断开收到 left。
func TestPeerRoster(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	a := New(Options{Nickname: "alice", Listener: la})
	t.Cleanup(a.Close)
	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}

	lb := &recordingListener{}
	b := New(Options{Nickname: "bob", Listener: lb})
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID

	// 断言 alice 的 peers 前，必须等 alice 自己处理完 bob 的 USER_JOIN；
	// 等待 bob 的事件无法保证 alice 侧已更新（两条独立连接，事件无跨端顺序保证）。
	waitFor(t, time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		return len(la.joined) >= 1
	})
	if got := a.GetState(); len(got.Peers) != 1 || got.Peers[0].Nickname != "bob" {
		t.Fatalf("alice peers = %+v", got.Peers)
	}

	b.Close()
	waitFor(t, 2*time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		return len(la.left) >= 1
	})
	if id := la.left[0]; id != bID {
		t.Fatalf("left id = %s, want %s", id, bID)
	}
}

// TestTextExchange 两客户端互发文本，收到事件。
func TestTextExchange(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	a := New(Options{Nickname: "alice", Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "bob", Listener: lb})
	t.Cleanup(b.Close)
	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	aID, bID := a.GetState().SelfID, b.GetState().SelfID
	_ = aID

	if _, err := a.SendText(bID, "hello bob", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(lb.messages) == 1
	})
	if lb.messages[0] != "hello bob" {
		t.Fatalf("msg = %q", lb.messages[0])
	}

	if _, err := b.SendText(aID, "hi alice", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		return len(la.messages) == 1
	})

	if _, err := a.SendText(bID, "", ""); err == nil {
		t.Fatal("empty text should fail")
	}
	// 向不存在的群组发送：信封可正常写出，服务器异步回 ERROR（非本地错误）。
	if _, err := a.SendText("", "x", "group1"); err != nil {
		t.Fatalf("group send write err = %v", err)
	}
}

// TestFileTransferDirect 端到端小文件直连传输。
func TestFileTransferDirect(t *testing.T) {
	url := startServer(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	content := make([]byte, 256*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	la := &recordingListener{}
	a := New(Options{Nickname: "sender", Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "receiver", Listener: lb})
	t.Cleanup(b.Close)
	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID

	tid, err := a.OfferFile(bID, srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// 接收方收到 offer 进度事件。
	waitFor(t, time.Second, func() bool {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(lb.progress) >= 1 && lb.progress[0].State == appapi.TransferPending
	})

	destPath := filepath.Join(dir, "dst.bin")
	if err := b.RespondFile(tid, true, destPath); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 5*time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(la.done) == 1 && len(lb.done) == 1
	})

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(content) {
		t.Fatalf("size = %d, want %d", len(got), len(content))
	}
	for i := range got {
		if got[i] != content[i] {
			t.Fatalf("byte %d mismatch", i)
		}
	}
}

// TestFileReject 接收方拒绝，发送方收到失败事件。
func TestFileReject(t *testing.T) {
	url := startServer(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	la := &recordingListener{}
	a := New(Options{Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Listener: lb})
	t.Cleanup(b.Close)
	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID

	tid, err := a.OfferFile(bID, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(lb.progress) >= 1
	})
	if err := b.RespondFile(tid, false, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		return len(la.failed) == 1
	})
}

// TestCancelFile 本地取消活动任务。
func TestCancelFile(t *testing.T) {
	url := startServer(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(srcPath, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	la := &recordingListener{}
	a := New(Options{Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Listener: lb})
	t.Cleanup(b.Close)
	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID

	tid, err := a.OfferFile(bID, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelFile(tid); err != nil {
		t.Fatal(err)
	}
	if err := a.CancelFile("missing"); err == nil {
		t.Fatal("cancel missing should fail")
	}
	if err := a.PauseFile(tid); err == nil {
		t.Fatal("pause should be unsupported")
	}
}

// TestM4Stubs 群组/频道方法在未连接时返回相应错误。
func TestM4Stubs(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	if _, err := c.OfferFileToGroup("g", "x"); err != errNotConnected {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.OfferFileToGroup("", "x"); err != errPeerNotFound {
		t.Fatalf("empty group err = %v", err)
	}
	if _, err := c.ChannelCreate("  ", "", false); err != errEmptyText {
		t.Fatalf("blank create err = %v", err)
	}
	if _, err := c.ChannelCreate("c", "", false); err == nil {
		t.Fatal("create on disconnected client should fail")
	}
	if err := c.ChannelJoin("c"); err == nil {
		t.Fatal("join on disconnected client should fail")
	}
	if err := c.ChannelJoin(""); err != errPeerNotFound {
		t.Fatalf("blank join err = %v", err)
	}
	if err := c.ChannelInvite("c", "p"); err == nil {
		t.Fatal("invite on disconnected client should fail")
	}
	if err := c.ChannelLeave("c"); err == nil {
		t.Fatal("leave on disconnected client should fail")
	}
	if chs := c.ChannelList(); chs == nil || len(chs) != 0 {
		t.Fatalf("channels = %v", chs)
	}
}

// TestNicknameAndDownloadDir 设置类方法。
func TestNicknameAndDownloadDir(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	if err := c.SetNickname("newname"); err != nil {
		t.Fatal(err)
	}
	if c.GetState().Nickname != "newname" {
		t.Fatal("nickname not updated")
	}
	if err := c.SetNickname(""); err == nil {
		t.Fatal("empty nickname should fail")
	}
	if err := c.PickDownloadDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := c.PickDownloadDir(""); err == nil {
		t.Fatal("empty dir should fail")
	}
}

// TestHistoryPersistenceAndMentions 验证 M10 本地聊天历史落盘恢复及 @提及 / 引用回复解析。
func TestHistoryPersistenceAndMentions(t *testing.T) {
	replyTo, mentions := extractMentionsAndReply("> [reply:m-42] 你好 @alice 和 @bob_1，@alice 请看")
	if replyTo != "m-42" {
		t.Fatalf("replyTo = %q, want m-42", replyTo)
	}
	if len(mentions) != 2 || mentions[0] != "alice" || mentions[1] != "bob_1" {
		t.Fatalf("mentions = %v, want [alice bob_1]", mentions)
	}

	dir := t.TempDir()
	c1 := New(Options{Nickname: "A", DownloadDir: dir})
	c1.history.append(appapi.ChatMessage{
		ID:       "msg-1",
		From:     "p1",
		To:       "p2",
		Type:     "TEXT_MSG",
		Text:     "[reply:msg-0] @bob hello",
		ReplyTo:  "msg-0",
		Mentions: []string{"bob"},
		TS:       1700000000000,
	})
	c1.Close()

	c2 := New(Options{Nickname: "A", DownloadDir: dir})
	t.Cleanup(c2.Close)
	st := c2.GetState()
	if len(st.Messages) != 1 || st.Messages[0].ID != "msg-1" || st.Messages[0].ReplyTo != "msg-0" {
		t.Fatalf("restored messages = %+v", st.Messages)
	}
}

// TestM11aRevokeReadAndSearch 验证消息已读回执、2 分钟内撤回与本地历史检索（含 JSONL 持久化重放）。
func TestM11aRevokeReadAndSearch(t *testing.T) {
	url := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	la := &recordingListener{}
	a := New(Options{Nickname: "Alice", DownloadDir: dirA, Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "Bob", DownloadDir: dirB, Listener: lb})
	t.Cleanup(b.Close)

	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	aID := a.GetState().SelfID
	bID := b.GetState().SelfID

	msgID, err := a.SendText(bID, "部署计划 v1.2 评审", "")
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(b.SearchHistory("部署计划", aID)) == 1
	})

	// Bob 发送已读回执（通过 /read 快捷指令或 MarkRead）。
	if _, err := b.SendText(aID, "/read "+msgID, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := a.GetState().Messages
		return len(msgs) == 1 && len(msgs[0].ReadBy) == 1 && msgs[0].ReadBy[0] == bID
	})

	// 非发送者尝试撤回他人消息应被拒绝。
	if err := b.RevokeMessage(aID, "", msgID); err == nil {
		t.Fatal("non-sender revoke should fail")
	}

	// Alice 撤回自己发送的消息。
	if _, err := a.SendText(bID, "/revoke "+msgID, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 1 && msgs[0].Revoked && msgs[0].Text == "[消息已撤回]"
	})

	// 撤回后的消息不应再被关键字检索命中。
	if got := b.SearchHistory("部署计划", aID); len(got) != 0 {
		t.Fatalf("revoked message should not match search, got %+v", got)
	}

	// 重启后从 JSONL 恢复，仍保持已读与已撤回状态。
	a2 := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a2.Close)
	restored := a2.GetState().Messages
	if len(restored) != 1 || !restored[0].Revoked || len(restored[0].ReadBy) != 1 {
		t.Fatalf("restored state after revoke/read mismatch: %+v", restored)
	}
}

// TestM11bDirectoryZipAndClipboardTransfer 验证 M11b 文件夹自动 ZIP 打包传输与剪贴板 base64 图片直传。
func TestM11bDirectoryZipAndClipboardTransfer(t *testing.T) {
	srcRoot := filepath.Join(t.TempDir(), "project-docs")
	if err := os.MkdirAll(filepath.Join(srcRoot, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "readme.txt"), []byte("hello folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "sub", "spec.md"), []byte("# spec"), 0o600); err != nil {
		t.Fatal(err)
	}

	zipInfo, err := openFileInfo(srcRoot)
	if err != nil {
		t.Fatalf("openFileInfo dir: %v", err)
	}
	_ = zipInfo.file.Close()
	if zipInfo.name != "project-docs.zip" || zipInfo.size <= 0 {
		t.Fatalf("unexpected zip fileInfo: %+v", zipInfo)
	}

	clipURI := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	clipInfo, err := openFileInfo(clipURI)
	if err != nil {
		t.Fatalf("openFileInfo clipboard: %v", err)
	}
	_ = clipInfo.file.Close()
	if clipInfo.name != "clipboard.png" || clipInfo.size <= 0 {
		t.Fatalf("unexpected clipboard fileInfo: %+v", clipInfo)
	}

	if _, err := openFileInfo("data:image/png;base64,"); err == nil {
		t.Fatal("empty clipboard base64 should fail")
	}
}

// TestM11cStaticServerDiscovery 验证 M11c 跨网段静态中心节点探测与 BrowseServers 合并。
func TestM11cStaticServerDiscovery(t *testing.T) {
	wsURL := startServer(t)
	t.Setenv("LANCHAT_STATIC_SERVERS", wsURL+",127.0.0.1:1")

	c := New(Options{Nickname: "Alice"})
	t.Cleanup(c.Close)

	servers := c.BrowseServers()
	var found bool
	for _, s := range servers {
		if strings.HasPrefix(s.ID, "static-") && s.AuthMode == "psk" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected static server in BrowseServers(), got %+v", servers)
	}
}

// TestM12AndM13QuoteReplyAndAutoBootstrap 验证 M12 引用回复指令 (/reply) 与 M13 无中心内嵌信令自举 ("auto")。
func TestM12AndM13QuoteReplyAndAutoBootstrap(t *testing.T) {
	a := New(Options{Nickname: "Alice"})
	t.Cleanup(a.Close)
	if err := a.Connect("auto", testPSK); err != nil {
		t.Fatalf("Connect(auto) failed: %v", err)
	}

	b := New(Options{Nickname: "Bob"})
	t.Cleanup(b.Close)
	a.mu.Lock()
	autoAddr := a.addr
	a.mu.Unlock()
	if err := b.Connect(autoAddr, testPSK); err != nil {
		t.Fatalf("Bob connect to auto server failed: %v", err)
	}

	aID := a.GetState().SelfID
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) >= 1 && len(b.GetState().Peers) >= 1
	})

	origID, err := a.SendText(bID, "今晚发布 v1.5.0", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Messages) == 1
	})

	replyID, err := b.SendText(aID, "/reply "+origID+" 收到，准时上线", "")
	if err != nil || replyID == "" {
		t.Fatalf("SendText /reply failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := a.GetState().Messages
		return len(msgs) == 2 && msgs[1].ReplyTo == origID && strings.Contains(msgs[1].Text, "今晚发布 v1.5.0")
	})

	// M13: 端到端文本加密 (/e2ee) —— 双方配置相同密钥时透明解密，密钥不匹配时显示加密占位。
	if status, err := a.SendText(bID, "/e2ee top-secret-key", ""); err != nil || status != "e2ee:on" {
		t.Fatalf("a /e2ee enable failed: status=%q err=%v", status, err)
	}
	if status, err := b.SendText(aID, "/e2ee top-secret-key", ""); err != nil || status != "e2ee:on" {
		t.Fatalf("b /e2ee enable failed: status=%q err=%v", status, err)
	}
	if _, err := a.SendText(bID, "绝密核验码 9527", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 3 && msgs[2].Text == "绝密核验码 9527"
	})
	if status, err := b.SendText(aID, "/e2ee off", ""); err != nil || status != "e2ee:off" {
		t.Fatalf("b /e2ee off failed: status=%q err=%v", status, err)
	}
	if _, err := a.SendText(bID, "未配置密钥不可读", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 4 && strings.HasPrefix(msgs[3].Text, "[加密消息]")
	})
}
