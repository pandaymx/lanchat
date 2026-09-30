package group

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// memStore 是测试用的内存 BlockSource：源预填内容，接收方逐块落盘。
type memStore struct {
	mu     sync.Mutex
	blocks map[int][]byte
	count  int
}

func newSourceStore(content []byte, size int) *memStore {
	m := &memStore{blocks: make(map[int][]byte), count: size}
	for i := 0; i < size; i++ {
		start := i * BlockSize
		end := start + BlockSize
		if end > len(content) {
			end = len(content)
		}
		m.blocks[i] = append([]byte(nil), content[start:end]...)
	}
	return m
}

func newRecvStore(count int) *memStore {
	return &memStore{blocks: make(map[int][]byte), count: count}
}

func (m *memStore) BlockCount() int { return m.count }

func (m *memStore) ReadBlock(idx int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[idx]
	if !ok {
		return nil, fmt.Errorf("block %d missing", idx)
	}
	return append([]byte(nil), b...), nil
}

func (m *memStore) WriteBlock(idx int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[idx] = append([]byte(nil), data...)
	return nil
}

func (m *memStore) assemble() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var buf bytes.Buffer
	for i := 0; i < m.count; i++ {
		b := m.blocks[i]
		// 除最后一块外都应恰为 BlockSize；防御性补齐避免拼接错位。
		if i < m.count-1 && len(b) < BlockSize {
			padded := make([]byte, BlockSize)
			copy(padded, b)
			b = padded
		}
		buf.Write(b)
	}
	return buf.Bytes()
}

// storedCount 返回已落盘块数（加锁，供轮询）。
func (m *memStore) storedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blocks)
}

// mesh 是全互联内存网络：按 ID 找到节点 swarm 并投递消息。
type mesh struct {
	mu      sync.Mutex
	nodes   map[string]*Swarm
	oneShot sync.Once
}

func newMesh() *mesh { return &mesh{nodes: make(map[string]*Swarm)} }

func (m *mesh) register(s *Swarm) {
	m.mu.Lock()
	m.nodes[s.selfID] = s
	m.mu.Unlock()
}

// Send 实现 Transport；异步投递避免持锁重入。
func (m *mesh) Send(msg Msg) {
	go func() {
		m.mu.Lock()
		target := m.nodes[msg.To]
		m.mu.Unlock()
		if target != nil {
			target.HandleMsg(msg)
		}
	}()
}

type recSink struct {
	lock   sync.Mutex
	events []MatrixEvent
}

func (r *recSink) OnMatrix(ev MatrixEvent) {
	r.lock.Lock()
	r.events = append(r.events, ev)
	r.lock.Unlock()
}
func (r *recSink) OnChannel(ChannelEvent) {}

func (r *recSink) finalMatrix() (MatrixEvent, bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if len(r.events) == 0 {
		return MatrixEvent{}, false
	}
	return r.events[len(r.events)-1], true
}

// buildDistribution 搭建 1 个源 + n 个接收方的 swarm 并启动。
func buildDistribution(t *testing.T, n, blockCount int, content []byte) (*mesh, string, []*memStore) {
	t.Helper()
	net := newMesh()
	srcStore := newSourceStore(content, blockCount)
	ids := make([]string, 0, n)
	recvIDs := make([]string, 0, n)
	recvStores := make([]*memStore, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("peer-%02d", i)
		recvIDs = append(recvIDs, id)
		recvStores = append(recvStores, newRecvStore(blockCount))
	}
	all := append(append(ids, "source"), recvIDs...)

	sink := &recSink{}
	source := NewSource("g1", "tx", "source", srcStore, recvIDs, net, sink)
	net.register(source)
	for i, id := range recvIDs {
		s := NewReceiver("g1", "tx", id, recvStores[i], all, []string{"source"}, net, sink)
		net.register(s)
	}
	source.Start()
	for _, id := range recvIDs {
		net.nodes[id].Start()
	}
	return net, fmt.Sprintf("%x", sha256.Sum256(content)), recvStores
}

func waitComplete(t *testing.T, recvStores []*memStore, wantHash string, blockCount int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, st := range recvStores {
			if st.storedCount() != blockCount {
				ok = false
				break
			}
		}
		if ok {
			for _, st := range recvStores {
				if fmt.Sprintf("%x", sha256.Sum256(st.assemble())) != wantHash {
					t.Fatal("接收内容哈希不一致")
				}
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("超时：仍有接收方未收齐全部块")
}

// sizedContent 生成长度恰为 n 的确定性内容（按字节循环填充）。
func sizedContent(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte("LANCHAT"[i%7])
	}
	return out
}

func TestSwarmN1DegradesToUnicast(t *testing.T) {
	content := sizedContent(BlockSize*3 + 10)
	_, wantHash, stores := buildDistribution(t, 1, 4, content)
	waitComplete(t, stores, wantHash, 4)
}

func TestSwarmFiveMembersAllComplete(t *testing.T) {
	content := sizedContent(BlockSize*2 + 123)
	net, wantHash, stores := buildDistribution(t, 5, 3, content)
	waitComplete(t, stores, wantHash, 3)

	// 源节点最终矩阵：5 名成员全部 Done。
	sink := net.nodes["source"].sink
	rs, ok := sink.(*recSink)
	if !ok {
		t.Fatal("sink 类型错误")
	}
	_ = rs
	if ev, ok := rs.finalMatrix(); !ok || ev.Done != 5 || ev.Total != 5 {
		t.Fatalf("最终矩阵 = %+v ok=%v, want Done=5 Total=5", ev, ok)
	}
}

// TestReceiverSeederUpgrade 验证完成成员可向其他成员供块：通过限制源
// 的供块能力无法在纯内存层直接表达，这里验证完成节点的位图全满、被
// 其他节点识别为 seeder（promoteSeeder 发送全量 BITFIELD）。
func TestReceiverSeederUpgrade(t *testing.T) {
	content := sizedContent(BlockSize + 7)
	net, _, stores := buildDistribution(t, 3, 2, content)
	waitComplete(t, stores, fmt.Sprintf("%x", sha256.Sum256(content)), 2)

	// 等待异步 BITFIELD 传播后，每个接收方都应被同伴视为 seeder。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allSeeders := true
		net.mu.Lock()
		for _, s := range net.nodes {
			if s.source {
				continue
			}
			s.mu.Lock()
			for _, m := range s.members {
				if m.have.count() != s.blocks {
					allSeeders = false
				}
			}
			s.mu.Unlock()
		}
		net.mu.Unlock()
		if allSeeders {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("超时：完成成员未被识别为 seeder")
}

func TestSortedDeterminism(t *testing.T) {
	ids := []string{"b", "a", "c"}
	sort.Strings(ids)
	if ids[0] != "a" {
		t.Fatal("排序失败")
	}
}
