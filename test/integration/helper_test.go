// Package integration 是 M1 信令闭环的进程内集成测试：
// 真实 TCP + WebSocket，服务端监听 127.0.0.1:0，客户端用 coder/websocket 拨号。
package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

// readTimeout 是测试中等待单条消息的默认上限。
const readTimeout = 3 * time.Second

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// testPSK 是集成测试统一使用的明文口令。
const testPSK = "test-psk"

// testClient 是一个测试侧 WS 客户端。
type testClient struct {
	t        *testing.T
	conn     *websocket.Conn
	id       string
	userList *protocol.Envelope // 握手阶段收到的全量在线表
	inbox    chan *protocol.Envelope
	done     chan struct{}
}

// startTestServer 用默认参数（端口 0）启动服务器，返回其 WS 入口地址。
func startTestServer(t *testing.T) string {
	t.Helper()
	return startTestServerOpts(t, nil)
}

// startTestServerOpts 以给定覆盖参数启动服务器（nil 表示全用默认）。
func startTestServerOpts(t *testing.T, mutate func(*server.Options)) string {
	t.Helper()
	hash, err := config.HashPSK(testPSK)
	if err != nil {
		t.Fatalf("生成测试 PSK 哈希: %v", err)
	}
	opts := server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           hash,
		HeartbeatInterval: 5 * time.Second,
		IdleTimeout:       15 * time.Second,
		ShutdownGrace:     time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}

	srv, err := server.New(opts, nil)
	if err != nil {
		t.Fatalf("New server: %v", err)
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

	// 等待监听就绪（ready 关闭与 ln 赋值建立 happens-before，读 ListenAddr 无竞争）。
	select {
	case <-srv.Ready():
	case <-time.After(time.Second):
		t.Fatal("服务器未在 1s 内开始监听")
	}
	addr := srv.ListenAddr()
	return "ws://" + addr + opts.Path
}

// dialClient 连接服务器、发 HELLO、读完握手阶段的 WELCOME 与 USER_LIST。
func dialClient(t *testing.T, url, psk, nickname string) *testClient {
	t.Helper()
	return dialClientWith(t, url, psk, nickname, uuid.NewString())
}

// dialClientWith 允许显式指定 deviceID（用于单点登录用例）。
func dialClientWith(t *testing.T, url, psk, nickname, deviceID string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c := &testClient{
		t:     t,
		conn:  conn,
		inbox: make(chan *protocol.Envelope, 64),
		done:  make(chan struct{}),
	}

	hello, err := protocol.NewEnvelope(uuid.NewString(), protocol.Hello, protocol.HelloPayload{
		Nickname: nickname,
		DeviceID: deviceID,
		PSK:      psk,
		OS:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	c.send(hello)

	welcome := c.readHandshake()
	if welcome.Type != protocol.Welcome {
		t.Fatalf("首帧 = %s，期望 WELCOME", welcome.Type)
	}
	var wp protocol.WelcomePayload
	if err := welcome.DecodePayload(&wp); err != nil {
		t.Fatal(err)
	}
	c.id = wp.SelfID
	if wp.HeartbeatInterval != 5 {
		t.Errorf("HeartbeatInterval = %d，期望 5", wp.HeartbeatInterval)
	}

	list := c.readHandshake()
	if list.Type != protocol.UserList {
		t.Fatalf("第二帧 = %s，期望 USER_LIST", list.Type)
	}
	c.userList = list

	// 握手完成后启动常驻 collector：用无超时 Read 收帧，避免读超时误关连接。
	go c.collect()
	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		<-c.done
	})
	return c
}

// collect 持续读取帧到 inbox，连接关闭即退出。
func (c *testClient) collect() {
	defer close(c.done)
	for {
		typ, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			return
		}
		env, err := protocol.UnmarshalEnvelope(data)
		if err != nil {
			return
		}
		c.inbox <- env
	}
}

// recv 在超时内从 inbox 取一条消息，无消息即失败（除非 optional）。
func (c *testClient) recv(d time.Duration) *protocol.Envelope {
	c.t.Helper()
	select {
	case env := <-c.inbox:
		return env
	case <-time.After(d):
		c.t.Fatalf("在 %s 内未等到消息", d)
		return nil
	}
}

// recvType 读取消息并断言其类型。
func (c *testClient) recvType(typ string) *protocol.Envelope {
	env := c.recv(readTimeout)
	if env.Type != typ {
		c.t.Fatalf("收到 %s，期望 %s", env.Type, typ)
	}
	return env
}

// recvUntil 持续取消息直到收到指定类型，期间跳过其它帧（如心跳 ACK），超时即失败。
func (c *testClient) recvUntil(typ string, d time.Duration) *protocol.Envelope {
	c.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case env := <-c.inbox:
			if env.Type == typ {
				return env
			}
		case <-deadline:
			c.t.Fatalf("在 %s 内未等到 %s", d, typ)
			return nil
		}
	}
}

// startHeartbeats 按 interval 周期发送 HEARTBEAT，返回停止函数。
func (c *testClient) startHeartbeats(interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.sendTo(protocol.Heartbeat, "", nil)
			case <-stop:
				return
			}
		}
	}()
	var stopOnce sync.Once
	stopFn := func() {
		stopOnce.Do(func() { close(stop) })
		<-done
	}
	return stopFn
}

// waitForClose 断言连接在 d 内被关闭（collector 退出）。
func (c *testClient) waitForClose(d time.Duration) {
	c.t.Helper()
	select {
	case <-c.done:
	case <-time.After(d):
		c.t.Fatalf("连接未在 %s 内关闭", d)
	}
}

// noMsg 断言 d 内没有任何新消息。
func (c *testClient) noMsg(d time.Duration) {
	c.t.Helper()
	select {
	case env := <-c.inbox:
		c.t.Fatalf("期望无消息，却收到 %s", env.Type)
	case <-time.After(d):
	}
}

// readHandshake 在 collector 启动前同步读取一帧，仅限握手阶段使用。
func (c *testClient) readHandshake() *protocol.Envelope {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("读取握手帧: %v", err)
	}
	if typ != websocket.MessageText {
		c.t.Fatalf("消息类型 = %v，期望文本帧", typ)
	}
	env, err := protocol.UnmarshalEnvelope(data)
	if err != nil {
		c.t.Fatalf("解码信封: %v", err)
	}
	return env
}

func (c *testClient) send(env *protocol.Envelope) {
	c.t.Helper()
	data, err := env.Marshal()
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("发送消息: %v", err)
	}
}

// sendTo 构造一条单播消息并发送。
func (c *testClient) sendTo(typ string, to string, payload any) *protocol.Envelope {
	c.t.Helper()
	env, err := protocol.NewEnvelope(uuid.NewString(), typ, payload)
	if err != nil {
		c.t.Fatal(err)
	}
	env.To = to
	c.send(env)
	return env
}

// userIDs 从 USER_LIST 负载提取全部在线 ID。
func userIDs(t *testing.T, env *protocol.Envelope) map[string]protocol.User {
	t.Helper()
	var p protocol.UserListPayload
	if err := env.DecodePayload(&p); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]protocol.User, len(p.Users))
	for _, u := range p.Users {
		out[u.ID] = u
	}
	return out
}
