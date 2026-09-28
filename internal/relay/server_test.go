package relay

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// startTestServer 启动一个短 TTL 的中继服务器并在测试结束时关闭。
func startTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	s.pairTTL = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.Serve(ctx) }()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// dialWithHandshake 连接中继并发送 relayID 握手行。
func dialWithHandshake(t *testing.T, addr, relayID string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	if _, err := c.Write([]byte(relayID + "\n")); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	return c
}

// TestPairAndForward 验证相同 relayID 的两端能配对并双向透传。
func TestPairAndForward(t *testing.T) {
	s := startTestServer(t)
	addr := s.Addr().String()

	const relayID = "relay-abc"
	a := dialWithHandshake(t, addr, relayID)
	defer a.Close()
	// 错开一点，保证 a 是先到者。
	time.Sleep(20 * time.Millisecond)
	b := dialWithHandshake(t, addr, relayID)
	defer b.Close()

	payload := []byte("hello through relay")
	var wg sync.WaitGroup
	wg.Add(2)

	// a 写，b 读。
	go func() {
		defer wg.Done()
		if _, err := a.Write(payload); err != nil {
			t.Errorf("a write: %v", err)
		}
	}()

	// b 写，a 读。
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		if _, err := b.Write([]byte("reply")); err != nil {
			t.Errorf("b write: %v", err)
		}
	}()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(b, got); err != nil {
		t.Fatalf("b read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("b got %q, want %q", got, payload)
	}

	reply := make([]byte, 5)
	if _, err := io.ReadFull(a, reply); err != nil {
		t.Fatalf("a read: %v", err)
	}
	if string(reply) != "reply" {
		t.Fatalf("a got %q, want reply", reply)
	}
	wg.Wait()
}

// TestDifferentIDsDoNotPair 验证不同 relayID 不串线：对端应只收到自己 ID 的数据。
func TestDifferentIDsDoNotPair(t *testing.T) {
	s := startTestServer(t)
	addr := s.Addr().String()

	x := dialWithHandshake(t, addr, "id-x")
	defer x.Close()
	y := dialWithHandshake(t, addr, "id-y")
	defer y.Close()

	// x 与 y 永远不会配对：先到的 x 会超时。
	if _, err := x.Write([]byte("stray")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = y.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	if n, err := y.Read(buf); err == nil {
		t.Fatalf("y unexpectedly received %d bytes: %q", n, buf[:n])
	}
}

// TestPairTimeout 验证单边到达后等待超时，服务器关闭其连接。
func TestPairTimeout(t *testing.T) {
	s := startTestServer(t)
	addr := s.Addr().String()

	c := dialWithHandshake(t, addr, "lonely")
	defer c.Close()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8)
	if _, err := c.Read(buf); err == nil {
		t.Fatalf("expected connection closed after timeout, got data")
	}
}

// TestSecondPairReusesRelayID 验证一个 relayID 配对完成后，
// 新的同 ID 两端仍能再次配对（waiting 项被正确清理）。
func TestSecondPairReusesRelayID(t *testing.T) {
	s := startTestServer(t)
	addr := s.Addr().String()

	pair := func(tag byte) {
		a := dialWithHandshake(t, addr, "rid")
		defer a.Close()
		time.Sleep(20 * time.Millisecond)
		b := dialWithHandshake(t, addr, "rid")
		defer b.Close()

		if _, err := a.Write([]byte{tag}); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := make([]byte, 1)
		if _, err := io.ReadFull(b, got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if got[0] != tag {
			t.Fatalf("got %d, want %d", got[0], tag)
		}
	}

	pair(1)
	pair(2)
}
