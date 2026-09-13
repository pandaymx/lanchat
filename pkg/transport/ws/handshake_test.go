package ws

// handshake_test.go 验证 client↔hub 传输加密端到端：
// 真实 WS 连接握手 → 加密帧往返 → TOFU 轮换拒绝。

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// testHubIdentity 生成临时服务端身份。
func testHubIdentity(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// freePort 拿一个空闲端口（与 hubserver 测试同策略：监听 :0 后释放）。
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestHandshakeRoundTrip：客户端连接加密 hub，握手后帧往返成功。
func TestHandshakeRoundTrip(t *testing.T) {
	key := testHubIdentity(t)
	addr := freePort(t)

	// 客户端信任回调：首次信任 + 记录。
	var trustedPub []byte
	trust := func(pub []byte) error {
		if len(trustedPub) == 0 {
			trustedPub = append([]byte(nil), pub...)
			return nil
		}
		if string(trustedPub) != string(pub) {
			return errors.New("TOFU violation")
		}
		return nil
	}

	// 起服务端（服务端加密 + 随机端口）。
	srv := New().WithPath("/ws").WithServerKey(key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotConn := make(chan core.Conn, 1)
	go func() {
		_ = srv.Listen(ctx, addr, func(conn core.Conn, _ protocol.Hello) error {
			select {
			case gotConn <- conn:
			default:
			}
			return nil
		})
	}()

	// 等端口可连。
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not listen on %s", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 客户端 Dial 加密连接。
	client := New().WithPath("/ws").WithClientTrust(trust)
	conn, err := client.Dial(ctx, "ws://"+addr, protocol.Hello{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("dial encrypted: %v", err)
	}
	defer conn.Close()
	if !encrypted(conn) {
		t.Fatal("client conn not encrypted")
	}

	// 服务端收到连接且已加密。
	var serverConn core.Conn
	select {
	case c := <-gotConn:
		serverConn = c
		if !encrypted(c) {
			t.Fatal("server conn not encrypted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not accept")
	}

	// 加密帧往返：客户端发 Hello，服务端 Recv 到同帧。
	if err := conn.Send(ctx, protocol.Frame{Kind: protocol.FKHello, Payload: []byte(`{"v":2}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := serverConn.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != protocol.FKHello || string(got.Payload) != `{"v":2}` {
		t.Fatalf("server got %s %s", got.Kind, got.Payload)
	}

	// TOFU 记录到 hub 公钥。
	if string(trustedPub) != string(key.PublicKey().Bytes()) {
		t.Fatal("client trusted wrong hub key")
	}
}

// encrypted 判断一条 core.Conn 是否已启用会话加密（避开类型断言遮蔽）。
func encrypted(c core.Conn) bool {
	type encChecker interface{ isEncrypted() bool }
	ec, ok := c.(encChecker)
	return ok && ec.isEncrypted()
}

// TestHandshakeTOFUReject：客户端已信任不同公钥 → 握手拒绝。
func TestHandshakeTOFUReject(t *testing.T) {
	key := testHubIdentity(t)
	addr := freePort(t)

	// 客户端信任回调：预置假公钥 → 每次调用都拒绝。
	trust := func(_ []byte) error {
		return errors.New("TOFU violation")
	}

	srv := New().WithPath("/ws").WithServerKey(key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = srv.Listen(ctx, addr, func(core.Conn, protocol.Hello) error { return nil })
	}()

	// 等端口。
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not listen on %s", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	client := New().WithPath("/ws").WithClientTrust(trust)
	if _, err := client.Dial(ctx, "ws://"+addr, protocol.Hello{DeviceID: "dev-2"}); err == nil {
		t.Fatal("dial with TOFU mismatch succeeded, want error")
	}
}
