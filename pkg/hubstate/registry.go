package hubstate

import (
	"context"
	"sync"
)

// Registry 是在线连接表，回答「一条消息该发给谁」。
//
// 结构刻意做成三层：**User → Device → 多个 Peer**（见 ADR-008）。
//
//	一个 User（人）可以有多台 Device（公司电脑 / 家里电脑 / 笔记本）
//	一台 Device 同时还可以有多条 Conn（断线重连期间新旧连接短暂共存）
//
// 一体的 IM 常在这里做「后登录的踢掉先登录的」，本项目明确不这么做——
// 用户诉求是多设备同时在线（AGENTS.md §7）。
//
// 之所以用 peerID 而不是 Peer 本身做 key：
// Peer 是接口，底层值可能不可比较（比如带 slice 字段的结构体指针除外，
// 但接口值比较仍会在动态类型不可比较时 panic）。用 Hub 分配的自增 ID 更稳。
type Registry struct {
	mu sync.RWMutex

	// conns 是全量连接表：peerID → 条目。
	conns map[uint64]*entry

	// byDevice 是 deviceID → 该设备的所有 peerID。
	byDevice map[string]map[uint64]struct{}

	// byUser 是 userID → 该用户的所有 peerID（ADR-008 的投递粒度）。
	// 它与 byDevice 冗余，但换来 O(1) 的「按用户广播」，而这是最频繁的操作。
	byUser map[string]map[uint64]struct{}

	next uint64
}

// entry 是注册表里的一条记录。
type entry struct {
	peer     Peer
	peerID   uint64
	deviceID string
	userID   string
	// helloOK 表示已收到合法的 FKHello。未握手前不参与消息投递，
	// 否则一个刚连上还没报身份的连接就会收到别人的历史消息。
	helloOK bool
}

// NewRegistry 构造一个空注册表。
func NewRegistry() *Registry {
	return &Registry{
		conns:    make(map[uint64]*entry),
		byDevice: make(map[string]map[uint64]struct{}),
		byUser:   make(map[string]map[uint64]struct{}),
	}
}

// Add 登记一条新连接，返回 Hub 分配的 peerID。
//
// 新条目的 helloOK 为 false：调用方在 FKHello 校验通过后必须调 MarkHello，
// 否则这条连接不会收到任何投递。
func (r *Registry) Add(p Peer, deviceID, userID string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.next++
	id := r.next
	r.conns[id] = &entry{
		peer:     p,
		peerID:   id,
		deviceID: deviceID,
		userID:   userID,
	}
	r.indexLocked(id, deviceID, userID)
	return id
}

// indexLocked 把 peerID 挂进 byDevice / byUser 两级索引。调用方必须持有写锁。
func (r *Registry) indexLocked(peerID uint64, deviceID, userID string) {
	if deviceID != "" {
		m := r.byDevice[deviceID]
		if m == nil {
			m = make(map[uint64]struct{})
			r.byDevice[deviceID] = m
		}
		m[peerID] = struct{}{}
	}
	if userID != "" {
		m := r.byUser[userID]
		if m == nil {
			m = make(map[uint64]struct{})
			r.byUser[userID] = m
		}
		m[peerID] = struct{}{}
	}
}

// unindexLocked 是 indexLocked 的逆操作，并在集合空时删掉整条 map 条目，
// 避免长期运行下 map 里堆满空集合。
func (r *Registry) unindexLocked(peerID uint64, deviceID, userID string) {
	if deviceID != "" {
		if m := r.byDevice[deviceID]; m != nil {
			delete(m, peerID)
			if len(m) == 0 {
				delete(r.byDevice, deviceID)
			}
		}
	}
	if userID != "" {
		if m := r.byUser[userID]; m != nil {
			delete(m, peerID)
			if len(m) == 0 {
				delete(r.byUser, userID)
			}
		}
	}
}

// MarkHello 记录该连接握手成功，并可按协议声明的身份改写索引。
//
// FKHello 里的身份才是权威的：transport 层预填的 deviceID 可能是临时值
// （比如 fake 用 "dev-3" 这类自增名）。所以握手后要重新索引一次。
func (r *Registry) MarkHello(peerID uint64, deviceID, userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.conns[peerID]
	if !ok {
		return
	}
	// 索引里的身份变了才需要重建索引，避免无意义的 map 操作。
	if e.deviceID != deviceID || e.userID != userID {
		r.unindexLocked(peerID, e.deviceID, e.userID)
		e.deviceID = deviceID
		e.userID = userID
		r.indexLocked(peerID, deviceID, userID)
	}
	e.helloOK = true
}

// Remove 注销一条连接。幂等：重复调用无副作用。
func (r *Registry) Remove(peerID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.conns[peerID]
	if !ok {
		return
	}
	r.unindexLocked(peerID, e.deviceID, e.userID)
	delete(r.conns, peerID)
}

// PeersForUser 返回该 User 的所有已握手连接（跨全部 Device）。
// 这是 ADR-008 的核心投递路径：**发给一个人 = 发给他的所有在线设备**。
func (r *Registry) PeersForUser(userID string) []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.collectLocked(r.byUser[userID])
}

// PeersForDevice 返回某台设备的所有已握手连接。
// 补发（FKHistoryResp）只发给请求的那一台设备，不扩散到同用户的其它设备。
func (r *Registry) PeersForDevice(deviceID string) []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.collectLocked(r.byDevice[deviceID])
}

// AllPeers 返回所有已握手连接，用于全局广播（如 Presence）。
func (r *Registry) AllPeers() []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Peer, 0, len(r.conns))
	for _, e := range r.conns {
		if e.helloOK {
			out = append(out, e.peer)
		}
	}
	return out
}

// collectLocked 把一组 peerID 转成 Peer 切片，跳过未握手的。调用方须持锁。
func (r *Registry) collectLocked(ids map[uint64]struct{}) []Peer {
	if len(ids) == 0 {
		return nil
	}
	out := make([]Peer, 0, len(ids))
	for id := range ids {
		if e, ok := r.conns[id]; ok && e.helloOK {
			out = append(out, e.peer)
		}
	}
	return out
}

// Count 返回当前登记的连接总数（含未握手的），用于测试与观测。
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// CloseAll 关闭并注销全部连接。用于 Hub 关停。
//
// 先快照再逐个关闭：Close 可能触发回调（进而再次调用 Remove），
// 持锁期间调用外部代码有死锁风险。
func (r *Registry) CloseAll() {
	r.mu.Lock()
	snapshot := make([]*entry, 0, len(r.conns))
	for _, e := range r.conns {
		snapshot = append(snapshot, e)
	}
	r.conns = make(map[uint64]*entry)
	r.byDevice = make(map[string]map[uint64]struct{})
	r.byUser = make(map[string]map[uint64]struct{})
	r.mu.Unlock()

	for _, e := range snapshot {
		_ = e.peer.Close()
	}
}

// SendToPeers 把一帧并发写给一组 Peer，返回第一个错误。
//
// 之所以并发而不是串行：一条慢连接（TCP 窗口满、对端不读）不应该
// 拖住其它设备的投递。ctx 取消时全体退出。
//
// 单个 Peer 写失败**不中断**其余投递——一台设备掉线不该影响别人收到消息。
func SendToPeers(ctx context.Context, peers []Peer, send func(context.Context, Peer) error) error {
	if len(peers) == 0 {
		return nil
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, p := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			if err := send(ctx, p); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	return first
}
