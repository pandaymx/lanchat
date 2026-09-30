package group

import (
	"sort"
	"sync"
)

// Transport 是 swarm 节点把块交换消息发出的出口；由外层注入。
// 测试中用内存 mesh，生产接线时按 To 选 P2P 直连或加密中继。
type Transport interface {
	Send(Msg)
}

// maxActiveSeeds 计算发送方并发供块上限：min(3, ceil(N/2))（方案 §9.4）。
func maxActiveSeeds(n int) int {
	if n < 2 {
		return 0
	}
	c := (n + 1) / 2
	if c > 3 {
		c = 3
	}
	return c
}

// Swarm 是一次文件 1:N 分发中单个节点（成员）的运行时状态。
//
// 源节点（source=true）持有全部块并供块；接收节点用位图记录进度，按
// rarest-first 向当前可得的种子请求缺失块，收齐后自动升级为 seeder。
// 所有方法并发安全；消息通过 HandleMsg 进入、Transport 发出。
type Swarm struct {
	groupID    string
	transferID string
	selfID     string
	source     bool

	src  BlockSource
	tr   Transport
	sink EventSink

	mu      sync.Mutex
	blocks  int
	have    *bitmap
	members map[string]*member
}

// NewSource 构造发送方（最初唯一 seeder）节点。recipients 为接收成员 ID。
// N=1 时退化为单份单播（见 Start），仍由本结构编排以便统一完成判定。
func NewSource(groupID, transferID, selfID string, src BlockSource, recipients []string, tr Transport, sink EventSink) *Swarm {
	s := &Swarm{
		groupID: groupID, transferID: transferID, selfID: selfID,
		source: true, src: src, tr: tr, sink: sink,
		have:    newBitmap(src.BlockCount()),
		members: make(map[string]*member, len(recipients)),
	}
	s.blocks = src.BlockCount()
	for i := 0; i < s.blocks; i++ {
		s.have.set(i)
	}
	for _, id := range recipients {
		s.members[id] = &member{id: id, have: newBitmap(s.blocks), pending: true}
	}
	return s
}

// NewReceiver 构造接收方节点。participants 为本次分发的全部成员 ID（含源
// 与其他接收方，用于块交换与 HAVE 通告），seeders 为当前已持有完整文件、
// 可立即供块的成员子集。
func NewReceiver(groupID, transferID, selfID string, src BlockSource, participants, seeders []string, tr Transport, sink EventSink) *Swarm {
	isSeeder := make(map[string]bool, len(seeders))
	for _, id := range seeders {
		isSeeder[id] = true
	}
	s := &Swarm{
		groupID: groupID, transferID: transferID, selfID: selfID,
		src: src, tr: tr, sink: sink,
		have:    newBitmap(src.BlockCount()),
		members: make(map[string]*member, len(participants)),
	}
	s.blocks = src.BlockCount()
	for _, id := range participants {
		if id == selfID {
			continue
		}
		s.members[id] = &member{id: id, have: newBitmap(s.blocks), seeder: isSeeder[id]}
	}
	return s
}

// Start 在成员集合确定后启动编排：源节点挑选首批接收者并发供块；
// 接收节点向种子通告自己的位图并请求首个块。N=1 时源只服务单个接收者，
// 语义等同单播（唯一接收者始终 active）。
func (s *Swarm) Start() {
	if s.source {
		s.startSource()
		return
	}
	s.broadcastBitfield()
	s.requestNext()
}

// startSource 选首批 active 接收者并向其推送 BITFIELD，其余排队。
func (s *Swarm) startSource() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.members))
	for id := range s.members {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 确定性顺序，便于测试
	activeN := maxActiveSeeds(len(ids))
	if len(ids) == 1 {
		activeN = 1 // N=1 退化单播
	}
	active := make(map[string]bool, activeN)
	for i, id := range ids {
		if i < activeN {
			active[id] = true
		}
	}
	s.mu.Unlock()

	for id := range active {
		s.sendBitfieldTo(id)
	}
}

// broadcastBitfield 接收方向所有种子通告自己的初始位图。
func (s *Swarm) broadcastBitfield() {
	s.mu.Lock()
	payload := encodeBitfield(s.blocks, s.have)
	var seeders []string
	for _, m := range s.members {
		if m.seeder {
			seeders = append(seeders, m.id)
		}
	}
	s.mu.Unlock()
	for _, id := range seeders {
		s.tr.Send(Msg{From: s.selfID, To: id, Frame: frameBitfield, Payload: payload})
	}
}

// sendBitfieldTo 源向单个接收者发送全量位图（源持有所有块），并将其
// 从排队中取出（标记为活跃）。
func (s *Swarm) sendBitfieldTo(to string) {
	s.mu.Lock()
	m, ok := s.members[to]
	if ok {
		m.pending = false
	}
	payload := encodeBitfield(s.blocks, s.have)
	s.mu.Unlock()
	s.tr.Send(Msg{From: s.selfID, To: to, Frame: frameBitfield, Payload: payload})
}

// HandleMsg 处理一条来自其他成员的块交换消息。
func (s *Swarm) HandleMsg(msg Msg) {
	switch msg.Frame {
	case frameBitfield:
		s.handleBitfield(msg)
	case frameRequest:
		s.handleRequest(msg)
	case frameBlock:
		s.handleBlock(msg)
	case frameHave:
		s.handleHave(msg)
	}
}

func (s *Swarm) handleBitfield(msg Msg) {
	count, bm, err := decodeBitfield(msg.Payload)
	if err != nil || count != s.blocks {
		return
	}
	s.mu.Lock()
	m, ok := s.members[msg.From]
	if ok {
		m.have = bm
		m.seeder = bm.count() == s.blocks
	}
	isSource := s.source
	var activate string
	if isSource && ok && bm.count() == s.blocks {
		// 接收方收齐：标记完成，若有排队成员则激活下一个。
		m.done = true
		activate = s.nextPendingLocked()
	}
	s.mu.Unlock()
	if activate != "" {
		s.sendBitfieldTo(activate)
	}
	if !isSource {
		// 接收方得知一个种子的持有情况，据此继续 rarest-first 请求。
		s.requestNext()
	}
	s.emitMatrix()
}

// nextPendingLocked 返回一个仍在排队（pending）的成员 ID；调用方持锁。
func (s *Swarm) nextPendingLocked() string {
	ids := make([]string, 0, len(s.members))
	for id, m := range s.members {
		if m.pending {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (s *Swarm) handleRequest(msg Msg) {
	idx, err := decodeBlockIdx(msg.Payload)
	if err != nil || idx < 0 || idx >= s.blocks {
		return
	}
	s.mu.Lock()
	canServe := s.have.has(idx)
	s.mu.Unlock()
	if !canServe {
		return
	}
	data, err := s.src.ReadBlock(idx)
	if err != nil {
		return
	}
	s.tr.Send(Msg{From: s.selfID, To: msg.From, Frame: frameBlock, Payload: encodeBlock(idx, data)})
}

func (s *Swarm) handleBlock(msg Msg) {
	idx, data, err := decodeBlock(msg.Payload)
	if err != nil || idx < 0 || idx >= s.blocks {
		return
	}
	s.mu.Lock()
	if s.have.has(idx) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := s.src.WriteBlock(idx, data); err != nil {
		return
	}

	s.mu.Lock()
	s.have.set(idx)
	if m, ok := s.members[msg.From]; ok {
		m.have.set(idx)
	}
	completed := s.have.count() == s.blocks
	havePayload := encodeBlockIdx(idx)
	var others []string
	for id := range s.members {
		if id != msg.From {
			others = append(others, id)
		}
	}
	s.mu.Unlock()

	// 通告新块可用（HAVE），帮助其余成员做 rarest-first 调度。
	for _, id := range others {
		s.tr.Send(Msg{From: s.selfID, To: id, Frame: frameHave, Payload: havePayload})
	}

	s.emitMatrix()
	if completed {
		s.promoteSeeder()
	} else {
		s.requestNext()
	}
}

func (s *Swarm) handleHave(msg Msg) {
	idx, err := decodeBlockIdx(msg.Payload)
	if err != nil {
		return
	}
	s.mu.Lock()
	if m, ok := s.members[msg.From]; ok {
		m.have.set(idx)
		if m.have.count() == s.blocks {
			m.seeder = true
		}
	}
	s.mu.Unlock()
	s.requestNext()
}

// promoteSeeder 收齐全部块后自动升级为 seeder，并通知对端更新位图。
func (s *Swarm) promoteSeeder() {
	s.mu.Lock()
	payload := encodeBitfield(s.blocks, s.have)
	var peers []string
	for id := range s.members {
		peers = append(peers, id)
	}
	s.mu.Unlock()
	for _, id := range peers {
		s.tr.Send(Msg{From: s.selfID, To: id, Frame: frameBitfield, Payload: payload})
	}
	s.emitMatrix()
}

// requestNext 按 rarest-first 选择一个缺失块，并向持有它的种子发起请求。
// 简化版稀缺统计：仅统计当前成员位图中各缺失块的持有者数，取最少者；
// 同稀缺度取最小下标，保证调度确定性。每个节点同时只挂一个在途请求。
func (s *Swarm) requestNext() {
	s.mu.Lock()
	if s.source {
		s.mu.Unlock()
		return
	}
	missing := s.have.missing(s.blocks)
	if len(missing) == 0 {
		s.mu.Unlock()
		return
	}
	// holders[block] = 当前持有该块、可供请求的成员 ID 列表。
	holders := make(map[int][]string, len(missing))
	for _, idx := range missing {
		for _, m := range s.members {
			if m.have.has(idx) {
				holders[idx] = append(holders[idx], m.id)
			}
		}
	}
	best := -1
	bestHolders := -1
	for _, idx := range missing {
		h := len(holders[idx])
		if h == 0 {
			continue
		}
		if best == -1 || h < bestHolders || (h == bestHolders && idx < best) {
			best = idx
			bestHolders = h
		}
	}
	var target string
	if best >= 0 {
		target = holders[best][0]
	}
	s.mu.Unlock()

	if best < 0 {
		return
	}
	s.tr.Send(Msg{From: s.selfID, To: target, Frame: frameRequest, Payload: encodeBlockIdx(best)})
}

// emitMatrix 计算成员矩阵并上抛（源节点视角）。
func (s *Swarm) emitMatrix() {
	s.mu.Lock()
	ev := MatrixEvent{
		GroupID: s.groupID, TransferID: s.transferID,
		HaveBitmap: s.have.bytes(),
		Total:      len(s.members),
	}
	if s.source {
		for _, m := range s.members {
			switch {
			case m.have.count() == s.blocks:
				ev.Done++
			case m.failed:
				ev.Failed++
			default:
				ev.Active++
			}
		}
	}
	s.mu.Unlock()
	if s.sink != nil && s.source {
		s.sink.OnMatrix(ev)
	}
}
