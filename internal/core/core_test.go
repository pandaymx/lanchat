package core

import (
	"context"
	"os"
	"path/filepath"
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

// recordingListener 记录全部事件供断言。
type recordingListener struct {
	mu       sync.Mutex
	conn     []appapi.ConnState
	joined   []appapi.Peer
	left     []string
	messages []string
	progress []appapi.Transfer
	done     []string
	failed   []string
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

func (l *recordingListener) OnMessageReceived(_, _, _, _, text string) {
	l.mu.Lock()
	l.messages = append(l.messages, text)
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
func (l *recordingListener) OnChannelUpdated([]appapi.Channel)    {}

func (l *recordingListener) snapshot() (conns []appapi.ConnState, msgs, done, failed []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]appapi.ConnState(nil), l.conn...),
		append([]string(nil), l.messages...),
		append([]string(nil), l.done...),
		append([]string(nil), l.failed...)
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
	if _, err := a.SendText(bID, "x", "group1"); err == nil {
		t.Fatal("group send should be not-implemented")
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

// TestM4Stubs 群组 / 频道诚实边界。
func TestM4Stubs(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	if _, err := c.OfferFileToGroup("g", "x"); err != errNotImplemented {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.ChannelCreate("c"); err != errNotImplemented {
		t.Fatalf("err = %v", err)
	}
	if err := c.ChannelJoin("c"); err != errNotImplemented {
		t.Fatalf("err = %v", err)
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
	if err := c.PickDownloadDir("/tmp/dl"); err != nil {
		t.Fatal(err)
	}
	if err := c.PickDownloadDir(""); err == nil {
		t.Fatal("empty dir should fail")
	}
}
