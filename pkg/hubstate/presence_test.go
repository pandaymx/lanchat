package hubstate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// helloFrame 构造一个 FKHello 帧，便于测试注入。
func helloFrame(user, device string) protocol.Frame {
	payload, _ := json.Marshal(protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		UserID:          user,
		DeviceID:        device,
	})
	return protocol.Frame{Kind: protocol.FKHello, Payload: payload}
}

// presenceOf 解析一帧的 Presence 负载。
func presenceOf(t *testing.T, f protocol.Frame) protocol.Presence {
	t.Helper()
	var pr protocol.Presence
	if err := json.Unmarshal(f.Payload, &pr); err != nil {
		t.Fatalf("decode presence: %v", err)
	}
	return pr
}

// TestPresence_RosterAndBroadcast 验证 M7.2 在线状态端到端语义：
//
//  1. alice 先上线，bob 后上线；
//  2. bob 握手后立刻收到 roster（含 alice online）+ 自己的上线回显；
//  3. alice 收到 bob 的 online 广播；
//  4. bob 断开后 alice 收到 bob 的 offline 广播。
func TestPresence_RosterAndBroadcast(t *testing.T) {
	r := NewRouter(nil)
	ctx := context.Background()

	alice := newPipePeer("dev-alice")
	bob := newPipePeer("dev-bob")
	r.Attach(ctx, alice)
	r.Attach(ctx, bob)

	alice.inject(helloFrame("alice", "dev-alice"))
	if !alice.waitFor(protocol.FKPresence, 1, time.Second) {
		t.Fatal("alice 握手后应收到自己的上线回显帧")
	}

	bob.inject(helloFrame("bob", "dev-bob"))

	// bob 应收到 roster（alice online）+ 自己回显，至少 2 帧 presence。
	if !bob.waitFor(protocol.FKPresence, 2, time.Second) {
		t.Fatalf("bob 握手后应收到 roster + 自己回显，实际 %d 帧", len(bob.framesOf(protocol.FKPresence)))
	}
	bobFrames := bob.framesOf(protocol.FKPresence)
	seen := map[string]bool{}
	for _, f := range bobFrames {
		pr := presenceOf(t, f)
		if !pr.Online {
			t.Fatal("roster/上线阶段不应出现 offline 帧")
		}
		seen[pr.DeviceID] = true
	}
	if !seen["dev-alice"] {
		t.Fatalf("bob 的 roster 里应包含 alice，实际 %v", seen)
	}
	if !seen["dev-bob"] {
		t.Fatalf("bob 应收到自己的上线回显，实际 %v", seen)
	}

	// alice 应收到 bob 的 online 广播（加上自己的回显共 2 帧 online）。
	if !alice.waitFor(protocol.FKPresence, 2, time.Second) {
		t.Fatalf("alice 应收到 bob 上线广播，实际 %d 帧", len(alice.framesOf(protocol.FKPresence)))
	}

	// bob 断开 → alice 收到 offline。
	if err := bob.Close(); err != nil {
		t.Fatal(err)
	}
	if !waitForCond(time.Second, func() bool {
		for _, f := range alice.framesOf(protocol.FKPresence) {
			pr := presenceOf(t, f)
			if pr.DeviceID == "dev-bob" && !pr.Online {
				return true
			}
		}
		return false
	}) {
		t.Fatal("bob 断开后 alice 应收到 dev-bob 的 offline 帧")
	}
}

// TestPresence_ReconnectNoFalseOffline 验证断线重连防抖：
// 同设备的新连接已入册时，旧连接离场不应广播 offline（设备其实还在线）。
func TestPresence_ReconnectNoFalseOffline(t *testing.T) {
	r := NewRouter(nil)
	ctx := context.Background()

	alice := newPipePeer("dev-alice")
	old := newPipePeer("dev-dup")
	r.Attach(ctx, alice)
	r.Attach(ctx, old)
	alice.inject(helloFrame("alice", "dev-alice"))
	old.inject(helloFrame("bob", "dev-dup"))
	if !alice.waitFor(protocol.FKPresence, 2, time.Second) {
		t.Fatalf("setup: alice 应收到自己回显 + bob 上线，实际 %d", len(alice.framesOf(protocol.FKPresence)))
	}

	// 同设备新连接接入（重连），先不断旧的。
	fresh := newPipePeer("dev-dup")
	r.Attach(ctx, fresh)
	fresh.inject(helloFrame("bob", "dev-dup"))
	if !fresh.waitFor(protocol.FKPresence, 1, time.Second) {
		t.Fatal("setup: 新连接握手未完成")
	}

	// 旧连接离场：此时 byDevice 里还有 fresh，不应广播 offline。
	before := len(alice.framesOf(protocol.FKPresence))
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if waitForCond(200*time.Millisecond, func() bool {
		return len(alice.framesOf(protocol.FKPresence)) > before
	}) {
		t.Fatal("旧连接离场但同设备仍在线，不应广播任何 presence 帧")
	}
}

// waitForCond 在 timeout 内轮询 cond，用于避免对「不应发生」的事件做固定 sleep。
func waitForCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
