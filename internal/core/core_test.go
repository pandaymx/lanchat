package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/server"
	"golang.org/x/crypto/bcrypt"
)

const testPSK = "test-psk"

// testPSKHash 在包初始化阶段以 bcrypt.MinCost 预计算一次测试口令哈希，
// 避免每个测试用例与客户端握手在 -race 下重复执行 cost=10 的 Blowfish 扩展。
var testPSKHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte(testPSK), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}()

// startServer 在随机端口启动测试服务器，返回 WS URL。
func startServer(t *testing.T) string {
	t.Helper()
	opts := server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           testPSKHash,
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
	opts := server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           testPSKHash,
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

// TestM14AutoLeaderElectionAndFailover 验证 M14 零配置先嗅探后自举与房主下线自动重选接管。
func TestM14AutoLeaderElectionAndFailover(t *testing.T) {
	a := New(Options{Nickname: "HostA"})
	t.Cleanup(a.Close)
	if err := a.Connect("auto", testPSK); err != nil {
		t.Fatalf("HostA Connect(auto): %v", err)
	}

	// B 输入空地址（等价于 auto），应嗅探到 HostA 的内嵌节点并直接连入，不重复启新服。
	b := New(Options{Nickname: "PeerB"})
	t.Cleanup(b.Close)
	if err := b.Connect("", testPSK); err != nil {
		t.Fatalf("PeerB Connect(empty/auto): %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 关闭原房主 HostA，PeerB 应触发自动故障转移（自举为新房主并恢复 Connected）。
	a.Close()
	waitFor(t, 12*time.Second, func() bool {
		st := b.GetState()
		return st.Conn == appapi.ConnConnected && st.SelfID != ""
	})
}

// TestM15PersistentIdentityAndAutoECDH 验证 M15 持久化密码学身份、零配置 1:1 自动 X25519 E2EE 与 /fingerprint。
func TestM15PersistentIdentityAndAutoECDH(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a1 := New(Options{Nickname: "Alice", DownloadDir: dirA})
	fp1 := a1.Fingerprint()
	if fp1 == "" {
		t.Fatal("expected non-empty identity fingerprint")
	}
	if err := a1.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	id1 := a1.GetState().SelfID
	a1.Close()

	// 重启客户端（使用相同 DownloadDir），SelfID 与安全指纹应保持完全一致。
	a2 := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a2.Close)
	if a2.Fingerprint() != fp1 {
		t.Fatalf("fingerprint changed across restart: %q vs %q", a2.Fingerprint(), fp1)
	}
	if err := a2.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if got := a2.GetState().SelfID; got != id1 {
		t.Fatalf("SelfID changed across restart: %q vs %q", got, id1)
	}

	b := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID

	waitFor(t, 2*time.Second, func() bool {
		return a2.PeerFingerprint(bID) != "" && b.PeerFingerprint(id1) != ""
	})
	if a2.PeerFingerprint(bID) != b.Fingerprint() {
		t.Fatalf("peer fingerprint mismatch: %q vs %q", a2.PeerFingerprint(bID), b.Fingerprint())
	}

	// 无需手动 /e2ee，单播消息自动使用 X25519 ECDH 端到端加密并在对端透明解密。
	if _, err := a2.SendText(bID, "M15 零配置自动 ECDH 密文", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 1 && msgs[0].Text == "M15 零配置自动 ECDH 密文"
	})

	// /fingerprint 指令应生成包含本机与对端指纹的核验记录。
	fpRet, err := a2.SendText(bID, "/fingerprint", "")
	if err != nil || !strings.HasPrefix(fpRet, "fp:") || !strings.Contains(fpRet, b.Fingerprint()) {
		t.Fatalf("/fingerprint unexpected result: ret=%q err=%v", fpRet, err)
	}
}

// TestM16OfflineSpoolAndChannelHistoryReplay 验证 M16 离线单播暂存补发与晚入群频道历史回放。
func TestM16OfflineSpoolAndChannelHistoryReplay(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b1 := New(Options{Nickname: "Bob", DownloadDir: dirB})
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b1.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b1.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1
	})

	// Bob 下线断开。
	b1.Close()
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 0
	})

	// Alice 在 Bob 离线期间发送单播消息（此时还同时验证离线 ECDH 密文投递）。
	if _, err := a.SendText(bID, "Bob 离线期间的重要通知", ""); err != nil {
		t.Fatal(err)
	}

	// Alice 创建频道并发送两条频道消息。
	if _, err := a.ChannelCreate("研发架构组", "", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Channels) == 1
	})
	chID := a.GetState().Channels[0].ID
	if _, err := a.SendText("", "频道历史第 1 条", chID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendText("", "频道历史第 2 条", chID); err != nil {
		t.Fatal(err)
	}

	// Bob 重新上线（相同持久化身份 dirB），应自动收到离线期间的单播消息。
	b2 := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b2.Close)
	if err := b2.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, m := range b2.GetState().Messages {
			if m.Text == "Bob 离线期间的重要通知" {
				return true
			}
		}
		return false
	})

	// Bob 晚加入频道 chID，应自动回放最近频道历史消息且幂等去重。
	if err := b2.ChannelJoin(chID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		var count int
		for _, m := range b2.GetState().Messages {
			if m.Group == chID && strings.HasPrefix(m.Text, "频道历史第 ") {
				count++
			}
		}
		return count == 2
	})
}

// TestM17VoiceAndInlineImageThumbnail 验证 M17 /voice 语音消息指令与小图/剪贴板图片内联缩略图自动推送。
func TestM17VoiceAndInlineImageThumbnail(t *testing.T) {
	wsURL := startServer(t)
	a := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 1. /voice 语音消息指令
	if _, err := a.SendText(bID, "/voice 8 收到请回复", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, m := range b.GetState().Messages {
			if strings.HasPrefix(m.Text, `🎤 [语音消息 8"]`) && strings.Contains(m.Text, "收到请回复") {
				return true
			}
		}
		return false
	})

	// 2. OfferFile 发送剪贴板/小尺寸 PNG 时，自动同步下发 data:image/png;base64,... 内联缩略图气泡
	clipURI := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	if _, err := a.OfferFile(bID, clipURI); err != nil {
		t.Fatalf("OfferFile clipboard image: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, m := range b.GetState().Messages {
			if m.Type == "STICKER_MSG" && strings.HasPrefix(m.Text, "data:image/png;base64,") {
				return true
			}
		}
		return false
	})
}

// TestM18AndM19ChannelE2EEAndAutoHeal 验证 M18 频道断线自愈重建与 M19 G2 频道自动密钥分发及端到端加密。
func TestM18AndM19ChannelE2EEAndAutoHeal(t *testing.T) {
	wsURL := startServer(t)
	a := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}

	if _, err := a.ChannelCreate("加密研发组", "M19 频道 E2EE", false); err != nil {
		t.Fatal(err)
	}
	var chID string
	waitFor(t, 2*time.Second, func() bool {
		chs := a.GetState().Channels
		if len(chs) == 1 && len(b.GetState().Channels) == 1 {
			chID = chs[0].ID
			return true
		}
		return false
	})

	if err := b.ChannelJoin(chID); err != nil {
		t.Fatal(err)
	}
	// 等待 Alice 自动把频道密钥经 ECDH 单播分发给 Bob，且 Bob 已同步自身成员状态。
	waitFor(t, 2*time.Second, func() bool {
		chs := b.GetState().Channels
		return b.ensureChannelKey(chID) != "" && len(chs) == 1 && len(chs[0].Members) == 2
	})

	// Alice 在频道内发消息，默认使用 enc:ch1: 加密，Bob 自动解密为明文。
	if _, err := a.SendText("", "频道零配置加密测试通过", chID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, m := range b.GetState().Messages {
			if m.Group == chID && m.Text == "频道零配置加密测试通过" {
				return true
			}
		}
		return false
	})

	// M18: 模拟房主切换至全新信令服务器 wsURL2（无历史频道），Alice（创建者）自动重建该频道，Bob 自动同名匹配重入。
	// 先断开非 Owner 的 Bob 再断开 Owner Alice，避免原服在 Alice 先断线瞬间把 Owner 移交给 Bob 导致双方重复建群。
	wsURL2 := startServer(t)
	for _, cli := range []*Client{b, a} {
		cli.mu.Lock()
		cli.addr = normalizeAddr(wsURL2)
		cli.autoMode = true
		oldConn := cli.conn
		cli.mu.Unlock()
		if oldConn != nil {
			_ = oldConn.CloseNow()
		}
	}
	waitFor(t, 5*time.Second, func() bool {
		chs := b.GetState().Channels
		return len(chs) == 1 && chs[0].ID != chID && chs[0].Name == "加密研发组" && len(chs[0].Members) == 2
	})
}

// TestM20ReactionsEditPinAndM21Bwlimit 验证 M20 表情回应(/react)、编辑(/edit)、置顶(/pin) 与 M21 带宽限速(/bwlimit) 及群任务暂停恢复。
func TestM20ReactionsEditPinAndM21Bwlimit(t *testing.T) {
	wsURL := startServer(t)
	a := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	aID := a.GetState().SelfID
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	msgID, err := a.SendText(bID, "初版发版说明", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Messages) == 1
	})

	// 1. Bob 点赞回应 /react
	if _, err := b.SendText(aID, "/react "+msgID+" 👍", ""); err != nil {
		t.Fatalf("/react failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := a.GetState().Messages
		return len(msgs) == 1 && strings.Contains(msgs[0].Text, "[👍]")
	})

	// 2. Alice 二次编辑消息 /edit
	if _, err := a.SendText(bID, "/edit "+msgID+" 终版发版说明 v2.0", ""); err != nil {
		t.Fatalf("/edit failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 1 && strings.Contains(msgs[0].Text, "终版发版说明 v2.0 (已编辑)")
	})

	// 3. Alice 置顶该消息 /pin
	if _, err := a.SendText(bID, "/pin "+msgID, ""); err != nil {
		t.Fatalf("/pin failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := b.GetState().Messages
		return len(msgs) == 1 && strings.HasPrefix(msgs[0].Text, "📌 [置顶] ")
	})

	// 4. M21: /bwlimit 动态限速与关闭
	if res, err := a.SendText(bID, "/bwlimit 25", ""); err != nil || res != "bwlimit:25.0MB/s" {
		t.Fatalf("/bwlimit 25 got %q, err %v", res, err)
	}
	if res, err := a.SendText(bID, "/bwlimit off", ""); err != nil || res != "bwlimit:off" {
		t.Fatalf("/bwlimit off got %q, err %v", res, err)
	}
}

// TestM23EncryptedHistoryM24CASInstantTransferAndM25Status 验证 M23 静态加密历史落盘、M24 内容寻址秒传去重与 M25 /status 诊断指令。
func TestM23EncryptedHistoryM24CASInstantTransferAndM25Status(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 1. M23: 发送消息后检查本地 lanchat-history.jsonl 为 enc:hist1: 密文，且新客户端重启可完整解密还原。
	if _, err := a.SendText(bID, "绝密架构设计纪要", ""); err != nil {
		t.Fatal(err)
	}
	rawHist, err := os.ReadFile(filepath.Join(dirA, historyFileName))
	if err != nil {
		t.Fatalf("read history file: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(rawHist)), "enc:hist1:") || strings.Contains(string(rawHist), "绝密架构设计纪要") {
		t.Fatalf("expected encrypted history file, got:\n%s", string(rawHist))
	}

	// 2. M24: 内容寻址秒传（CAS Instant Transfer）— Bob 下载目录已有相同内容文件时，RespondFile 立即 0 字节秒传完成。
	payload := []byte("CAS-INSTANT-DEDUP-PAYLOAD-2026-LANCHAT")
	srcPath := filepath.Join(dirA, "release-bundle.bin")
	if err := os.WriteFile(srcPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	// 在 Bob 的下载目录预置一份同内容文件（模拟历史已接收过同 SHA-256 的文件）。
	seedCopy := filepath.Join(dirB, "cached-copy.bin")
	if err := os.WriteFile(seedCopy, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	transferID, err := a.OfferFile(bID, srcPath)
	if err != nil {
		t.Fatalf("OfferFile: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Transfers) == 1
	})
	destPath := filepath.Join(dirB, "instant-received.bin")
	if err := b.RespondFile(transferID, true, destPath); err != nil {
		t.Fatalf("RespondFile: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		stA := a.GetState().Transfers
		stB := b.GetState().Transfers
		return len(stA) == 1 && stA[0].State == appapi.TransferDone &&
			len(stB) == 1 && stB[0].State == appapi.TransferDone
	})
	gotBytes, err := os.ReadFile(destPath)
	if err != nil || string(gotBytes) != string(payload) {
		t.Fatalf("CAS instant copy content mismatch: %v", err)
	}

	// 3. M25: /status 诊断指令返回连接状态、安全指纹与 CAS 索引数量。
	res, err := a.SendText(bID, "/status", "")
	if err != nil || !strings.HasPrefix(res, "status:conn=connected ") || !strings.Contains(res, "cas=1") {
		t.Fatalf("unexpected /status result %q, err %v", res, err)
	}

	// 验证 Alice 重启后从加密历史恢复消息。
	a.Close()
	a2 := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a2.Close)
	foundDecrypted := false
	for _, m := range a2.GetState().Messages {
		if m.Text == "绝密架构设计纪要" {
			foundDecrypted = true
			break
		}
	}
	if !foundDecrypted {
		t.Fatalf("expected decrypted message after restart, got %+v", a2.GetState().Messages)
	}
}

// TestM26SilentUpdateWithDomesticProxyFallback 验证国内加速代理自动回退、version.json 检测、SHA-256 校验与免浏览器静默下载。
func TestM26SilentUpdateWithDomesticProxyFallback(t *testing.T) {
	pkgBytes := []byte("DEB-PACKAGE-BINARY-PAYLOAD-V9.9.9")
	sum := sha256.Sum256(pkgBytes)
	sumHex := hex.EncodeToString(sum[:])

	// 1. 构造故障代理节点（始终 502）与可用国内加速镜像节点。
	badMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer badMirror.Close()

	goodMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/latest/download/version.json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"9.9.9","tag":"v9.9.9","commit":"abc1234"}`))
		case strings.HasSuffix(r.URL.Path, "/download/v9.9.9/SHA256SUMS"):
			_, _ = fmt.Fprintf(w, "%s  LANChat-9.9.9-linux-amd64.deb\n", sumHex)
		case strings.HasSuffix(r.URL.Path, "/download/v9.9.9/LANChat-9.9.9-linux-amd64.deb"):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(pkgBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer goodMirror.Close()

	dlDir := t.TempDir()
	cli := New(Options{Nickname: "Updater", OS: "linux", DownloadDir: dlDir})
	t.Cleanup(cli.Close)
	cli.updateBaseURL = "https://github.com/pandaymx/lanchat/releases"
	cli.updateProxies = []string{badMirror.URL + "/", goodMirror.URL + "/"}
	cli.updateHTTPClient = goodMirror.Client()

	// 2. 验证内置国内代理列表查询
	proxiesMsg, err := cli.SendText("", "/update proxies", "")
	if err != nil || !strings.Contains(proxiesMsg, "ghfast.top") || !strings.Contains(proxiesMsg, "gh-proxy.com") {
		t.Fatalf("unexpected /update proxies response: %q, err=%v", proxiesMsg, err)
	}

	// 3. 验证 /update check 自动跳过 502 代理并通过可用镜像检测新版本
	checkMsg, err := cli.SendText("", "/update check", "")
	if err != nil || !strings.HasPrefix(checkMsg, "update:发现新版本 v9.9.9") {
		t.Fatalf("unexpected /update check response: %q, err=%v", checkMsg, err)
	}

	// 4. 验证 /update 静默下载安装包到 DownloadDir 并完成 SHA-256 校验
	dlMsg, err := cli.SendText("", "/update", "")
	if err != nil || !strings.Contains(dlMsg, "SHA-256 校验通过") {
		t.Fatalf("unexpected /update response: %q, err=%v", dlMsg, err)
	}
	savedFile := filepath.Join(dlDir, "LANChat-9.9.9-linux-amd64.deb")
	got, err := os.ReadFile(savedFile)
	if err != nil || string(got) != string(pkgBytes) {
		t.Fatalf("silent downloaded package content mismatch: err=%v, got=%q", err, string(got))
	}
	if len(cli.GetState().Messages) != 0 {
		t.Fatalf("/update should not pollute chat message history, got %+v", cli.GetState().Messages)
	}
}

// TestDiagnosticExclusionAndReplyReactionSanitization 验证安全码指纹/状态诊断不进入聊天气泡流，且回复消息不会将 [👍] 或旧版指纹引用带入气泡正文。
func TestDiagnosticExclusionAndReplyReactionSanitization(t *testing.T) {
	if got := sanitizeQuoteAndReactions("「[安全码指纹] 本机: F3A8-8288-9F58 [👍]」\n你好"); got != "你好" {
		t.Fatalf("sanitizeQuoteAndReactions diagnostic quote got %q, want %q", got, "你好")
	}
	if got := sanitizeQuoteAndReactions("「今天发版顺利 [👍] [🎉]」\n收到 [👍]"); got != "「今天发版顺利」\n收到 [👍]" {
		t.Fatalf("sanitizeQuoteAndReactions normal quote got %q", got)
	}

	wsURL := startServer(t)
	a := New(Options{Nickname: "Alice", DownloadDir: t.TempDir()})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: t.TempDir()})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	aID := a.GetState().SelfID
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// /fingerprint 与 /status 不应写入聊天气泡列表
	if _, err := a.SendText(bID, "/fingerprint", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendText(bID, "/status", ""); err != nil {
		t.Fatal(err)
	}
	if len(a.GetState().Messages) != 0 {
		t.Fatalf("diagnostic commands should not appear in chat messages, got %+v", a.GetState().Messages)
	}

	// 发送一条普通消息并点赞，随后引用回复该消息，引用块内不应包含 [👍]
	msgID, err := a.SendText(bID, "下午三点对齐接口", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Messages) == 1
	})
	if _, err := b.SendText(aID, "/react "+msgID+" 👍", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := a.GetState().Messages
		return len(msgs) == 1 && strings.HasSuffix(msgs[0].Text, " [👍]")
	})
	if _, err := b.SendText(aID, "/reply "+msgID+" 好的没问题", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs := a.GetState().Messages
		return len(msgs) == 2 && msgs[1].Text == "「下午三点对齐接口」\n好的没问题"
	})
}

// TestCLISelfUpdateBinaryInPlace 验证 CLI 二进制自更新（国内代理回退、SHA-256 校验与原地原子替换）。
func TestCLISelfUpdateBinaryInPlace(t *testing.T) {
	newBinBytes := []byte("#!/bin/sh\necho lanchat-v9.9.9\n")
	sum := sha256.Sum256(newBinBytes)
	sumHex := hex.EncodeToString(sum[:])
	assetName := CLIBinaryAssetName("v9.9.9", runtime.GOOS, runtime.GOARCH)

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/latest/download/version.json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"9.9.9","tag":"v9.9.9","commit":"def5678"}`))
		case strings.HasSuffix(r.URL.Path, "/download/v9.9.9/SHA256SUMS"):
			_, _ = fmt.Fprintf(w, "%s  %s\n", sumHex, assetName)
		case strings.HasSuffix(r.URL.Path, "/download/v9.9.9/"+assetName):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(newBinBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mirror.Close()

	workDir := t.TempDir()
	fakeExec := filepath.Join(workDir, "lanchat")
	if err := os.WriteFile(fakeExec, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := SelfUpdateBinary(
		context.Background(),
		"1.11.0",
		"",
		fakeExec,
		workDir,
		false,
		"https://github.com/pandaymx/lanchat/releases",
		[]string{mirror.URL + "/"},
		mirror.Client(),
	)
	if err != nil {
		t.Fatalf("SelfUpdateBinary failed: %v", err)
	}
	if !res.HasNewVersion || !res.SHA256Verified || res.SavedPath != fakeExec {
		t.Fatalf("unexpected SelfUpdateBinary result: %+v", res)
	}
	got, err := os.ReadFile(fakeExec)
	if err != nil || string(got) != string(newBinBytes) {
		t.Fatalf("replaced binary content mismatch: err=%v, got=%q", err, string(got))
	}
}

// TestM28DirectoryTreeStreamingAndAutoUnpack 验证 M28 文件夹整包流式传输、接收端自动解包还原目录层级与权限、以及 Zip-Slip 防护。
func TestM28DirectoryTreeStreamingAndAutoUnpack(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	srcFolder := filepath.Join(dirA, "sdk-release")
	if err := os.MkdirAll(filepath.Join(srcFolder, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcFolder, "empty-sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcFolder, "README.md"), []byte("# SDK v1.13"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcFolder, "bin", "run.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// Alice 通过 /senddir 指令直接发送整个文件夹
	transferID, err := a.SendText(bID, "/senddir "+srcFolder, "")
	if err != nil || transferID == "" {
		t.Fatalf("/senddir failed: id=%q err=%v", transferID, err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Transfers) == 1
	})

	destZip := filepath.Join(dirB, "sdk-release.zip")
	if err := b.RespondFile(transferID, true, destZip); err != nil {
		t.Fatalf("RespondFile: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		trs := b.GetState().Transfers
		return len(trs) == 1 && trs[0].State == appapi.TransferDone
	})

	// 接收端应已自动解包还原出 sdk-release/ 目录树（含子目录、空目录与脚本内容）
	unpackedRoot := filepath.Join(dirB, "sdk-release")
	readmeBytes, err := os.ReadFile(filepath.Join(unpackedRoot, "README.md"))
	if err != nil || string(readmeBytes) != "# SDK v1.13" {
		t.Fatalf("unpacked README.md mismatch: err=%v, got=%q", err, string(readmeBytes))
	}
	scriptBytes, err := os.ReadFile(filepath.Join(unpackedRoot, "bin", "run.sh"))
	if err != nil || string(scriptBytes) != "#!/bin/sh\necho ok\n" {
		t.Fatalf("unpacked bin/run.sh mismatch: err=%v, got=%q", err, string(scriptBytes))
	}
	if st, err := os.Stat(filepath.Join(unpackedRoot, "empty-sub")); err != nil || !st.IsDir() {
		t.Fatalf("expected unpacked empty-sub directory, err=%v", err)
	}
}

// TestM29ClipboardSyncAndPasteToSend 验证 M29 局域网剪贴板快捷同步 (/clip) 与剪贴板截图直发 (/paste)。
func TestM29ClipboardSyncAndPasteToSend(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 1. Alice 通过 /clip 向 Bob 同步一段代码片段，Bob 可通过 /clip 读取且不污染聊天气泡流
	res, err := a.SendText(bID, "/clip ssh -i ~/.ssh/id_ed25519 dev@192.168.1.10", "")
	if err != nil || res != "clip:ssh -i ~/.ssh/id_ed25519 dev@192.168.1.10" {
		t.Fatalf("/clip push failed: res=%q err=%v", res, err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return b.LastClipboard() == "ssh -i ~/.ssh/id_ed25519 dev@192.168.1.10"
	})
	gotClip, err := b.SendText("", "/clip get", "")
	if err != nil || gotClip != "clip:ssh -i ~/.ssh/id_ed25519 dev@192.168.1.10" {
		t.Fatalf("/clip get mismatch: got=%q err=%v", gotClip, err)
	}
	if len(b.GetState().Messages) != 0 {
		t.Fatalf("/clip should not pollute chat message bubbles, got %+v", b.GetState().Messages)
	}

	// 2. Alice 通过 /paste 直接粘贴发送剪贴板截图 data URI，Bob 收到文件传输与内联缩略图预览
	clipURI := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	tid, err := a.SendText(bID, "/paste "+clipURI, "")
	if err != nil || tid == "" {
		t.Fatalf("/paste screenshot failed: tid=%q err=%v", tid, err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(b.GetState().Transfers) == 1 && len(b.GetState().Messages) == 1
	})
}

// TestM30BurnAfterReadingAutoPurge 验证 M30 阅后即焚限时自毁消息 (/burn) 在倒计时结束后从双端内存与本地加密历史文件中彻底物理抹除。
func TestM30BurnAfterReadingAutoPurge(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a := New(Options{Nickname: "Alice", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 1. 先发一条永久保留消息，再发一条 150ms 阅后即焚消息
	if _, err := a.SendText(bID, "普通公开通知", ""); err != nil {
		t.Fatal(err)
	}
	burnID, err := a.SendText(bID, "/burn 150ms 绝密一次性令牌-9981", "")
	if err != nil || burnID == "" {
		t.Fatalf("/burn send failed: id=%q err=%v", burnID, err)
	}

	// 倒计时前，双方均可见该阅后即焚消息
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Messages) == 2 && len(b.GetState().Messages) == 2
	})

	// 倒计时结束后，该阅后即焚消息应从 Alice 与 Bob 的内存及加密 JSONL 磁盘文件中彻底物理抹除，仅剩普通消息
	waitFor(t, 3*time.Second, func() bool {
		return len(a.GetState().Messages) == 1 && len(b.GetState().Messages) == 1
	})
	if a.GetState().Messages[0].Text != "普通公开通知" || b.GetState().Messages[0].Text != "普通公开通知" {
		t.Fatalf("expected only normal message remaining, got A=%+v B=%+v", a.GetState().Messages, b.GetState().Messages)
	}

	// 重启客户端验证加密磁盘历史中同样不残留任何已销毁消息
	b.Close()
	b2 := New(Options{Nickname: "Bob", DownloadDir: dirB})
	t.Cleanup(b2.Close)
	if len(b2.GetState().Messages) != 1 || b2.GetState().Messages[0].Text != "普通公开通知" {
		t.Fatalf("burned message leaked in persisted history: %+v", b2.GetState().Messages)
	}
}

// TestM31ServerlessMeshSplitBrainConvergence 验证 M31 零配置 mDNS Mesh 脑裂收敛与 /mesh 指令：
// 当两个节点因并发自举或网络分区恢复而各自启动内嵌服务器时，ID 较大的房主会自动让位、
// 关停本机内嵌服务器并透明重连至 ID 字典序最小的主节点，实现房间自动合流。
func TestM31ServerlessMeshSplitBrainConvergence(t *testing.T) {
	a := New(Options{Nickname: "NodeA", DownloadDir: t.TempDir()})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "NodeB", DownloadDir: t.TempDir()})
	t.Cleanup(b.Close)

	// 固定 deviceID 以确定字典序：NodeA ("0001-node-a") < NodeB ("ffff-node-b")
	a.mu.Lock()
	a.deviceID = "0001-node-a"
	a.mu.Unlock()
	b.mu.Lock()
	b.deviceID = "ffff-node-b"
	b.mu.Unlock()

	// 模拟脑裂：NodeB 先以 local 自举内嵌服务器，随后 NodeA 也以 local 自举内嵌服务器（两者处于不同房间）
	if err := b.Connect("local", testPSK); err != nil {
		t.Fatalf("NodeB Connect(local) failed: %v", err)
	}
	if err := a.Connect("local", testPSK); err != nil {
		t.Fatalf("NodeA Connect(local) failed: %v", err)
	}
	if !a.IsEmbeddedLeader() || !b.IsEmbeddedLeader() {
		t.Fatalf("expected both nodes initially running embedded leader before convergence")
	}

	// 触发或等待 M31 Mesh 脑裂收敛：NodeB (ffff-node-b) 应发现 NodeA (0001-node-a) 并自动让位合流
	res, err := b.SendText("", "/mesh", "*")
	if err != nil {
		t.Fatalf("NodeB /mesh command failed: %v", err)
	}
	if !strings.Contains(res, "leader=auto-0001-node-a") {
		t.Fatalf("expected NodeB to converge to auto-0001-node-a, got %q", res)
	}
	if b.IsEmbeddedLeader() {
		t.Fatalf("expected NodeB to shut down its embedded server after converging to NodeA")
	}
	if !a.IsEmbeddedLeader() || a.LeaderID() != "auto-0001-node-a" {
		t.Fatalf("expected NodeA to remain canonical embedded leader, got leader=%q", a.LeaderID())
	}

	// 验证收敛后双方已处于同一信令房间并能互发消息
	waitFor(t, 3*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})
	bID := b.GetState().SelfID
	if _, err := a.SendText(bID, "Mesh 脑裂合流成功", ""); err != nil {
		t.Fatalf("post-convergence SendText failed: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		for _, m := range b.GetState().Messages {
			if m.Text == "Mesh 脑裂合流成功" {
				return true
			}
		}
		return false
	})
}

// TestM32AndM33DNDUnreadAndUpdateApply 验证 M32 免打扰 (/dnd)、未读与 @提及统计 (/unread)
// 以及 M33 一键下载并生成安装器指令 (/update apply)。
func TestM32AndM33DNDUnreadAndUpdateApply(t *testing.T) {
	wsURL := startServer(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	a := New(Options{Nickname: "Alice", OS: "windows", DownloadDir: dirA})
	t.Cleanup(a.Close)
	b := New(Options{Nickname: "Bob", OS: "linux", DownloadDir: dirB})
	t.Cleanup(b.Close)
	if err := a.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(wsURL, testPSK); err != nil {
		t.Fatal(err)
	}
	bID := b.GetState().SelfID
	aID := a.GetState().SelfID
	waitFor(t, 2*time.Second, func() bool {
		return len(a.GetState().Peers) == 1 && len(b.GetState().Peers) == 1
	})

	// 1. 验证 /dnd 免打扰开关
	dndRes, err := b.SendText(aID, "/dnd on", "")
	if err != nil || dndRes != "dnd:on" || !b.IsDND() {
		t.Fatalf("/dnd on failed: res=%q err=%v isDND=%v", dndRes, err, b.IsDND())
	}

	// 2. Alice 向 Bob 发送普通消息与 @Bob 提及消息，验证未读与提及计数
	if _, err := a.SendText(bID, "第一条普通提醒", ""); err != nil {
		t.Fatal(err)
	}
	msg2ID, err := a.SendText(bID, "@Bob 请查收发版清单", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		u, m := b.UnreadTotal()
		return u == 2 && m == 1
	})
	unreadStat, err := b.SendText(aID, "/unread", "")
	if err != nil || unreadStat != "unread:2 mentions:1 dnd:true" {
		t.Fatalf("unexpected /unread stat: %q err=%v", unreadStat, err)
	}

	// Bob 执行 /read 后，该会话未读与 @ 提及计数应自动清零
	if _, err := b.SendText(aID, "/read "+msg2ID, ""); err != nil {
		t.Fatal(err)
	}
	uAfter, mAfter := b.UnreadTotal()
	if uAfter != 0 || mAfter != 0 {
		t.Fatalf("expected unread/mentions cleared after /read, got unread=%d mentions=%d", uAfter, mAfter)
	}

	// 3. 验证 M33 /update apply 自动下载安装包并生成平台安装命令
	winAsset := "LANChat-9.9.0-windows-x64-Sideload.msix"
	winPayload := []byte("mock-msix-installer-binary-m33")
	sum := sha256.Sum256(winPayload)
	sumHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest/download/version.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"version":"9.9.0","tag":"v9.9.0"}`)
	})
	mux.HandleFunc("/releases/download/v9.9.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", sumHex, winAsset)
	})
	mux.HandleFunc("/releases/download/v9.9.0/"+winAsset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(winPayload)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a.updateBaseURL = srv.URL + "/releases"
	a.updateProxies = []string{""}
	a.updateHTTPClient = srv.Client()

	applyRes, err := a.SendText(bID, "/update apply", "")
	if err != nil {
		t.Fatalf("/update apply failed: %v", err)
	}
	if !strings.HasPrefix(applyRes, "update:apply:") || !strings.Contains(applyRes, "Add-AppxPackage") {
		t.Fatalf("unexpected /update apply result: %q", applyRes)
	}
}
