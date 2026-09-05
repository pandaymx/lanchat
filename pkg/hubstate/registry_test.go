package hubstate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakePeer 是最小 Peer 实现，只记录收到的帧。
type fakePeer struct {
	mu     sync.Mutex
	sent   []int // 收到的帧 Kind 序列
	closed bool
}

func (p *fakePeer) Recv(context.Context) (protocol.Frame, error) {
	return protocol.Frame{}, errors.New("fakePeer.Recv 不该被调用")
}

func (p *fakePeer) Send(_ context.Context, f protocol.Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("closed")
	}
	p.sent = append(p.sent, int(f.Kind))
	return nil
}

func (p *fakePeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakePeer) Closed() <-chan struct{} { return nil }

func (p *fakePeer) DeviceID() string { return "" }

func (p *fakePeer) sentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

func TestRegistryAddRemoveCount(t *testing.T) {
	r := NewRegistry()
	p1 := &fakePeer{}
	p2 := &fakePeer{}

	id1 := r.Add(p1, "dev-1", "u-1")
	id2 := r.Add(p2, "dev-2", "u-1")
	if r.Count() != 2 {
		t.Fatalf("Add 后应有 2 条，实际 %d", r.Count())
	}

	r.Remove(id1)
	if r.Count() != 1 {
		t.Fatalf("Remove 后应剩 1 条，实际 %d", r.Count())
	}

	// 重复 Remove 幂等
	r.Remove(id1)
	if r.Count() != 1 {
		t.Fatalf("重复 Remove 应幂等，实际剩 %d", r.Count())
	}

	r.Remove(id2)
	if r.Count() != 0 {
		t.Fatalf("全部 Remove 后应为 0，实际 %d", r.Count())
	}
}

// TestRegistryUnhelloedNotDelivered 验证未握手的连接收不到投递。
// 这是一条安全性质：连上还没报身份就收历史消息等于信息泄露。
func TestRegistryUnhelloedNotDelivered(t *testing.T) {
	r := NewRegistry()
	p := &fakePeer{}
	r.Add(p, "dev-1", "u-1")

	if got := len(r.PeersForUser("u-1")); got != 0 {
		t.Fatalf("未握手不应出现在 PeersForUser，实际 %d", got)
	}
	if got := len(r.AllPeers()); got != 0 {
		t.Fatalf("未握手不应出现在 AllPeers，实际 %d", got)
	}

	// 握手后可见
	id := uint64(1)
	r.MarkHello(id, "dev-1", "u-1")
	if got := len(r.PeersForUser("u-1")); got != 1 {
		t.Fatalf("握手后 PeersForUser 应有 1 条，实际 %d", got)
	}
}

// TestRegistryMarkHelloReindex 验证握手时会按协议身份重建索引。
func TestRegistryMarkHelloReindex(t *testing.T) {
	r := NewRegistry()
	p := &fakePeer{}
	// 登记时是 transport 层的临时身份
	id := r.Add(p, "tmp-dev", "")

	if got := len(r.PeersForDevice("tmp-dev")); got != 0 {
		t.Fatalf("临时身份且未握手，不应可投递，实际 %d", got)
	}

	r.MarkHello(id, "real-dev", "u-9")

	// 旧身份失效
	if got := len(r.PeersForDevice("tmp-dev")); got != 0 {
		t.Fatalf("旧 deviceID 索引应已清除，实际仍有 %d", got)
	}
	// 新身份生效
	if got := len(r.PeersForDevice("real-dev")); got != 1 {
		t.Fatalf("新 deviceID 应能找到 1 条，实际 %d", got)
	}
	if got := len(r.PeersForUser("u-9")); got != 1 {
		t.Fatalf("userID u-9 应能找到 1 条，实际 %d", got)
	}
}

// TestRegistryMultiDeviceFanout 验证 ADR-008 的核心：一个 User 的多台设备都能收到。
func TestRegistryMultiDeviceFanout(t *testing.T) {
	r := NewRegistry()
	devices := []*fakePeer{
		{}, {}, {}, // u-1 的三台设备
	}
	other := &fakePeer{} // 另一个人

	for i, p := range devices {
		id := r.Add(p, string(rune('a'+i)), "u-1")
		r.MarkHello(id, string(rune('a'+i)), "u-1")
	}
	otherID := r.Add(other, "dev-x", "u-2")
	r.MarkHello(otherID, "dev-x", "u-2")

	peers := r.PeersForUser("u-1")
	if len(peers) != 3 {
		t.Fatalf("u-1 应有 3 台设备在线，实际 %d", len(peers))
	}

	if err := SendToPeers(context.Background(), peers, func(ctx context.Context, p Peer) error {
		return p.Send(ctx, protocol.Frame{Kind: protocol.FKDeliver})
	}); err != nil {
		t.Fatalf("SendToPeers: %v", err)
	}

	for i, p := range devices {
		if p.sentCount() != 1 {
			t.Fatalf("设备 %d 应收到 1 帧，实际 %d", i, p.sentCount())
		}
	}
	if other.sentCount() != 0 {
		t.Fatalf("无关用户不应收到，实际 %d", other.sentCount())
	}
}

// TestRegistryMultiConnPerDevice 验证同一设备的多条连接共存（重连期间）。
func TestRegistryMultiConnPerDevice(t *testing.T) {
	r := NewRegistry()
	oldConn := &fakePeer{}
	newConn := &fakePeer{}

	id1 := r.Add(oldConn, "dev-1", "u-1")
	r.MarkHello(id1, "dev-1", "u-1")
	id2 := r.Add(newConn, "dev-1", "u-1")
	r.MarkHello(id2, "dev-1", "u-1")

	if got := len(r.PeersForDevice("dev-1")); got != 2 {
		t.Fatalf("同一设备两条连接应都在，实际 %d", got)
	}

	// 旧连接断开后，新连接仍在 —— 这就是「不互踢」
	r.Remove(id1)
	if got := len(r.PeersForDevice("dev-1")); got != 1 {
		t.Fatalf("旧连接移除后应剩 1 条，实际 %d", got)
	}
}

// TestRegistryCloseAll 验证关停时全部连接被关闭并注销。
func TestRegistryCloseAll(t *testing.T) {
	r := NewRegistry()
	ps := []*fakePeer{{}, {}, {}}
	for i, p := range ps {
		id := r.Add(p, string(rune('a'+i)), "u-1")
		r.MarkHello(id, string(rune('a'+i)), "u-1")
	}

	r.CloseAll()

	if r.Count() != 0 {
		t.Fatalf("CloseAll 后应无连接，实际 %d", r.Count())
	}
	for i, p := range ps {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if !closed {
			t.Fatalf("连接 %d 应已被 Close", i)
		}
	}
}

// TestRegistryIndexesCleanedUp 验证索引条目会被清理，避免长期运行内存膨胀。
func TestRegistryIndexesCleanedUp(t *testing.T) {
	r := NewRegistry()
	p := &fakePeer{}
	id := r.Add(p, "dev-1", "u-1")
	r.MarkHello(id, "dev-1", "u-1")
	r.Remove(id)

	r.mu.RLock()
	_, devLeft := r.byDevice["dev-1"]
	_, userLeft := r.byUser["u-1"]
	r.mu.RUnlock()

	if devLeft {
		t.Error("Remove 后 byDevice 里的空集合应被删除")
	}
	if userLeft {
		t.Error("Remove 后 byUser 里的空集合应被删除")
	}
}
