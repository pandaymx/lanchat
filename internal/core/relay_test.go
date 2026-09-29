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
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// startRelayServer 启动带数据面中继的服务器，返回 WS URL。
func startRelayServer(t *testing.T) string {
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

// connectPair 建立两个已连接的核心，返回二者与其连接和监听器。
func connectPair(t *testing.T, url string) (*Client, *Client, *recordingListener, *recordingListener) {
	t.Helper()
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
	return a, b, la, lb
}

// TestFileTransferRelay 经真实服务器与真实中继端到端传输：
// 任务建立后由接收方发 RELAY_REQUEST，双方拿到 GRANT 后直驱 grant 编排，
// 覆盖 ECDH 密钥协商、RELAY_KEY 转发与 RelaySend/RelayReceive。
func TestFileTransferRelay(t *testing.T) {
	url := startRelayServer(t)
	a, b, la, lb := connectPair(t, url)
	aID, bID := a.GetState().SelfID, b.GetState().SelfID

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	content := make([]byte, 300*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := openFileInfo(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := hashFile(info.file, info.size)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := transfer.GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}

	const tid = "relay-task-1"
	// 发送方任务（出站）。
	st, err := newTransferTask(a, tid, appapi.TransferOutbound, bID, info.name, info.size)
	if err != nil {
		t.Fatal(err)
	}
	st.file = info.file
	st.senderPriv = priv
	st.sha256 = sum
	st.token = "relay-token"
	if !a.addTransfer(st) {
		t.Fatal("sender task already exists")
	}

	// 接收方任务（入站）。
	rt, err := newTransferTask(b, tid, appapi.TransferInbound, aID, info.name, info.size)
	if err != nil {
		t.Fatal(err)
	}
	rt.sha256 = sum
	rt.token = "relay-token"
	rt.senderECDH = transfer.EncodePub(priv.PublicKey())
	rt.destPath = filepath.Join(dir, "dst.bin")
	if !b.addTransfer(rt) {
		t.Fatal("receiver task already exists")
	}

	// 接收方发起中继请求，服务器向双方下发相同 GRANT。
	bConn, err := b.connectedConn()
	if err != nil {
		t.Fatal(err)
	}
	req, err := b.sendTo(bConn, protocol.RelayRequest, "", protocol.RelayRequestPayload{
		TransferID: tid,
		PeerID:     aID,
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.relayRequest = req.ID

	var grant protocol.RelayGrantPayload
	waitFor(t, 2*time.Second, func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		if len(st.ch) == 0 {
			return false
		}
		s := <-st.ch
		if s.typ != protocol.RelayGrant {
			return false
		}
		return s.env.DecodePayload(&grant) == nil
	})

	// 服务器向双方各发一份 GRANT：接收方这份在直驱前需排空，
	// 否则会被 runPath 当控制信令立即取消中继路径（生产中由 inboundRelayRequest 消费）。
	waitFor(t, 2*time.Second, func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.ch) > 0
	})
	rt.mu.Lock()
	if s := <-rt.ch; s.typ != protocol.RelayGrant {
		rt.mu.Unlock()
		t.Fatalf("unexpected signal %s", s.typ)
	}
	rt.mu.Unlock()

	// 并发直驱双方 grant 编排：接收方回 RELAY_KEY，发送方等待并解出文件密钥。
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.inboundRelayGrant(rt, bConn, grant)
	}()
	go func() {
		defer wg.Done()
		a.outboundRelayGrant(st, grant)
	}()

	waitFor(t, 5*time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(la.done) == 1 && len(lb.done) == 1
	})
	wg.Wait()
	_ = info.file.Close()

	got, err := os.ReadFile(rt.destPath)
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

// TestRelayRequestUndeliverable 对端不在线时 RELAY_REQUEST 收到 undeliverable ACK。
func TestRelayRequestUndeliverable(t *testing.T) {
	url := startServer(t)
	l := &recordingListener{}
	c := New(Options{Listener: l})
	t.Cleanup(c.Close)
	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	conn, err := c.connectedConn()
	if err != nil {
		t.Fatal(err)
	}
	task, err := newTransferTask(c, "t1", appapi.TransferInbound, "ghost", "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	c.addTransfer(task)

	err = c.inboundRelayRequest(task, conn)
	if err == nil {
		t.Fatal("expected undeliverable error")
	}
}

// TestRelayGrantEmptyAddr grant 未带地址时任务失败。
func TestRelayGrantEmptyAddr(t *testing.T) {
	url := startRelayServer(t)
	a, b, _, _ := connectPair(t, url)
	aID, bID := a.GetState().SelfID, b.GetState().SelfID

	st, err := newTransferTask(a, "t2", appapi.TransferOutbound, bID, "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	a.addTransfer(st)
	a.outboundRelayGrant(st, protocol.RelayGrantPayload{TransferID: "t2"})
	if st.snapshot().State != appapi.TransferFailed {
		t.Fatalf("state = %s", st.snapshot().State)
	}

	rt, err := newTransferTask(b, "t3", appapi.TransferInbound, aID, "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	b.addTransfer(rt)
	bConn, _ := b.connectedConn()
	b.inboundRelayGrant(rt, bConn, protocol.RelayGrantPayload{TransferID: "t3"})
	if rt.snapshot().State != appapi.TransferFailed {
		t.Fatalf("state = %s", rt.snapshot().State)
	}
}

// TestStickerExchange 互发表情，接收方以名称收到事件。
func TestStickerExchange(t *testing.T) {
	url := startServer(t)
	a, b, _, lb := connectPair(t, url)
	aID, bID := a.GetState().SelfID, b.GetState().SelfID

	if _, err := a.SendSticker(bID, "smile.png"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		return len(lb.messages) == 1
	})
	if lb.messages[0] != "smile.png" {
		t.Fatalf("sticker = %q", lb.messages[0])
	}

	if _, err := b.SendSticker("", "x"); err == nil {
		t.Fatal("empty to should fail")
	}
	if _, err := b.SendSticker(aID, ""); err == nil {
		t.Fatal("empty path should fail")
	}
}

// TestPresenceUpdate 收到 PRESENCE_UPDATE 刷新对端资料。
func TestPresenceUpdate(t *testing.T) {
	url := startServer(t)
	a, b, la, _ := connectPair(t, url)
	bID := b.GetState().SelfID

	waitFor(t, time.Second, func() bool {
		la.mu.Lock()
		defer la.mu.Unlock()
		return len(la.joined) >= 1
	})
	if err := b.SetNickname("bobby"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.peers[bID].Nickname == "bobby"
	})
}

// TestBrowseServers 懒启动浏览并返回切片（无 mDNS 通告时为空，不阻塞）。
func TestBrowseServers(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	if s := c.BrowseServers(); s == nil {
		t.Fatal("expected non-nil slice")
	}
	// 再次调用不应重复启动浏览 goroutine。
	_ = c.BrowseServers()
}

// TestPayloadError ERROR 信封解码为普通错误。
func TestPayloadError(t *testing.T) {
	env, err := protocol.NewEnvelope("id1", protocol.Error, protocol.ErrorPayload{
		Code: "bad", Message: "boom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e := payloadError(env); e == nil || e.Error() != "boom" {
		t.Fatalf("err = %v", e)
	}

	env2, err := protocol.NewEnvelope("id2", protocol.Error, protocol.ErrorPayload{Code: "onlycode"})
	if err != nil {
		t.Fatal(err)
	}
	if e := payloadError(env2); e == nil || e.Error() != "onlycode" {
		t.Fatalf("err = %v", e)
	}
}

// TestErrString 错误短原因映射。
func TestErrString(t *testing.T) {
	if s := errString(nil); s != "" {
		t.Fatalf("nil err = %q", s)
	}
	if s := errString(transfer.ErrDialFailed); s != "direct connection failed" {
		t.Fatalf("dial err = %q", s)
	}
	if s := errString(errInvalidState); s != "core: operation not allowed in current state" {
		t.Fatalf("err = %q", s)
	}
}

// TestSetListener 替换回调后事件走新监听器。
func TestSetListener(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	l := &recordingListener{}
	c.SetListener(l)
	c.listener().OnConnChanged(appapi.ConnConnected, "")
	if len(l.conn) != 1 {
		t.Fatalf("conn events = %d", len(l.conn))
	}
}

// TestPeerNickname 查询对端昵称。
func TestPeerNickname(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	c.peers["p1"] = appapi.Peer{ID: "p1", Nickname: "n1"}
	if got := c.peerNickname("p1"); got != "n1" {
		t.Fatalf("nickname = %q", got)
	}
	if got := c.peerNickname("missing"); got != "" {
		t.Fatalf("missing nickname = %q", got)
	}
}

// TestNoopListener 未注册回调时使用丢弃实现，各方法可安全调用。
func TestNoopListener(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	l := c.listener()
	if _, ok := l.(noopListener); !ok {
		t.Fatalf("listener = %T, want noopListener", l)
	}
	l.OnConnChanged(appapi.ConnConnected, "")
	l.OnPeerJoined(appapi.Peer{ID: "x"})
	l.OnPeerLeft("x")
	l.OnMessageReceived("a", "g", "m", protocol.TextMsg, "t")
	l.OnTransferProgress(appapi.Transfer{ID: "x"})
	l.OnTransferDone("x")
	l.OnTransferFailed("x", "r")
	l.OnGroupMatrix("g", "m", nil)
	l.OnChannelUpdated(nil)
}

// TestResumeFile 恢复在 M4 不支持。
func TestResumeFile(t *testing.T) {
	c := New(Options{})
	t.Cleanup(c.Close)
	if err := c.ResumeFile("missing"); err != errTransferGone {
		t.Fatalf("missing err = %v", err)
	}
	task, err := newTransferTask(c, "t", appapi.TransferOutbound, "p", "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	c.addTransfer(task)
	if err := c.ResumeFile("t"); err != errPauseUnsupported {
		t.Fatalf("err = %v", err)
	}
}

// TestOpenFileInfoErrors 文件打开的错误分支。
func TestOpenFileInfoErrors(t *testing.T) {
	if _, err := openFileInfo(""); err != errEmptyPath {
		t.Fatalf("empty err = %v", err)
	}
	if _, err := openFileInfo(filepath.Join(t.TempDir(), "missing.bin")); err != errFileNotFound {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := openFileInfo(t.TempDir()); err == nil {
		t.Fatal("directory should fail")
	}
	if n := defaultNickname(); n == "" {
		t.Fatal("empty default nickname")
	}
}

// TestRouteErrorAndAck ERROR / ACK 按 relayRequest 匹配回投任务。
func TestRouteErrorAndAck(t *testing.T) {
	url := startServer(t)
	c := New(Options{})
	t.Cleanup(c.Close)
	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	task, err := newTransferTask(c, "t", appapi.TransferInbound, "p", "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	task.relayRequest = "req-1"
	c.addTransfer(task)

	errEnv, err := protocol.NewEnvelope("e1", protocol.Error, protocol.ErrorPayload{Code: "x", Message: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	errEnv.ReplyTo = "req-1"
	c.routeError(errEnv)
	if s := <-task.ch; s.typ != protocol.Error {
		t.Fatalf("signal = %s", s.typ)
	}

	ackEnv, err := protocol.NewEnvelope("a1", protocol.MsgAck, protocol.MsgAckPayload{Status: "undeliverable"})
	if err != nil {
		t.Fatal(err)
	}
	ackEnv.ReplyTo = "req-1"
	c.routeAck(ackEnv)
	if s := <-task.ch; s.typ != protocol.MsgAck {
		t.Fatalf("signal = %s", s.typ)
	}

	if e := fmtErrAuth(""); e != errAuthFailed {
		t.Fatalf("empty reason err = %v", e)
	}
	if e := fmtErrAuth("bad psk"); e == nil || e.Error() == "" {
		t.Fatalf("reason err = %v", e)
	}
}
