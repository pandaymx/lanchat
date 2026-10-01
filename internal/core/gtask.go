package core

import (
	"encoding/binary"
	"sync"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/group"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// groupTask 编排一次群组文件分发（G1 全体或 G2 频道）。
//
// group.Swarm 的成员集合在构造时固定、无动态增删，故异步 JOIN/LEAVE 由本
// actor 维护「候选地址 + 最新位图」名册，并在成员集合变化时重建 Swarm；
// 重建后重放各成员最新位图以恢复下载/供块状态。所有事件串行处理，无锁竞态。
type groupTask struct {
	cli      *Client
	groupID  string
	id       string
	name     string
	size     int64
	sha      string
	source   bool
	peerName string // 入站任务的源 ID（对齐 Transfer.PeerID）

	mu     sync.Mutex
	src    *fileBlockSource
	tr     *blockTransport
	swarm  *group.Swarm
	events chan groupEvent
	closed bool // stop() 后置位，禁止再向 events 投递

	// cached 由 actor 在每次推进后更新，供 GetState 跨 goroutine 读取（避免
	// 与 actor 并发访问 cand/bm 等 map）。
	snapMu sync.Mutex
	cached appapi.Transfer

	// roster：成员（含源）候选地址、对端签发的握手 token 与最近一次块位图。
	cand map[string][]string
	tok  map[string]string
	bm   map[string][]byte

	// doneIDs 已收齐的成员；completed 为本机是否完成（接收方）。
	doneIDs   map[string]bool
	completed bool
}

// groupEvent 是交给 actor 串行处理的一条事件。
type groupEvent struct {
	kind groupEventKind
	from string
	// 信令（JOIN/LEAVE）或块帧使用：
	env     *protocol.Envelope
	frame   byte
	payload []byte
}

type groupEventKind int

const (
	evJoin groupEventKind = iota + 1
	evLeave
	evBlock
	evProgress
	evDown
)

func newSourceGroupTask(cli *Client, groupID, id, name string, size int64, sha string, src *fileBlockSource, tr *blockTransport) *groupTask {
	return &groupTask{
		cli: cli, groupID: groupID, id: id, name: name, size: size, sha: sha,
		source: true, src: src, tr: tr,
		events:  make(chan groupEvent, 64),
		cand:    map[string][]string{},
		tok:     map[string]string{},
		bm:      map[string][]byte{},
		doneIDs: map[string]bool{},
	}
}

func newReceiverGroupTask(cli *Client, groupID, id, name string, size int64, sha string, srcID string) *groupTask {
	return &groupTask{
		cli: cli, groupID: groupID, id: id, name: name, size: size, sha: sha,
		source: false, peerName: srcID,
		events:  make(chan groupEvent, 64),
		cand:    map[string][]string{},
		tok:     map[string]string{},
		bm:      map[string][]byte{},
		doneIDs: map[string]bool{},
	}
}

// run 是 actor 主循环：串行处理 JOIN/LEAVE/块帧，保证重建与消息无竞态。
func (g *groupTask) run() {
	for ev := range g.events {
		switch ev.kind {
		case evJoin:
			g.handleJoin(ev)
		case evLeave:
			g.handleLeave(ev)
		case evBlock:
			g.handleBlock(ev)
		case evProgress:
			g.handleProgress(ev)
		case evDown:
			g.handleDown(ev)
		}
	}
}

func (g *groupTask) stop() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	close(g.events)
	g.mu.Unlock()
}

// post 把事件安全投递给 actor：缓冲满时后台等待，stop 后静默丢弃，绝不向
// 已关闭 channel 发送，也不阻塞 WebSocket 收泵。
func (g *groupTask) post(ev groupEvent) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	ch := g.events
	g.mu.Unlock()

	select {
	case ch <- ev:
	default:
		go func() {
			g.mu.Lock()
			if g.closed {
				g.mu.Unlock()
				return
			}
			ch = g.events
			g.mu.Unlock()
			ch <- ev
		}()
	}
}

// deliverBlock 由块连接读泵回调：把帧转成 actor 事件串行处理。
func (g *groupTask) deliverBlock(from string, frame byte, payload []byte) {
	g.post(groupEvent{kind: evBlock, from: from, frame: frame, payload: payload})
}

// handleJoin 纳入新成员候选并重建 Swarm（源与接收方都维护完整名册）。
func (g *groupTask) handleJoin(ev groupEvent) {
	var p protocol.GroupJoinPayload
	if ev.env == nil || ev.env.DecodePayload(&p) != nil {
		return
	}
	if _, known := g.cand[ev.from]; known {
		return
	}
	g.cand[ev.from] = p.Candidates
	g.tok[ev.from] = p.Token
	if g.tr != nil {
		g.tr.setCandidates(ev.from, p.Candidates)
		g.tr.setPeerToken(ev.from, p.Token)
	}
	g.rebuild()
}

// handleLeave 移除成员并重建；本机为接收方且离开的是源时放弃本次分发。
func (g *groupTask) handleLeave(ev groupEvent) {
	if !g.source && ev.from == g.peerName {
		g.cli.removeGroupTask(g.id)
		g.teardown()
		return
	}
	delete(g.cand, ev.from)
	delete(g.tok, ev.from)
	delete(g.bm, ev.from)
	delete(g.doneIDs, ev.from)
	g.rebuild()
}

// handleBlock 把块连接上收到的帧交给当前 Swarm，并记录对端最新位图。
func (g *groupTask) handleBlock(ev groupEvent) {
	if g.swarm == nil {
		return
	}
	if ev.frame == protocol.FrameBitfield && len(ev.payload) >= 4 {
		g.bm[ev.from] = ev.payload[4:]
	}
	g.swarm.HandleMsg(group.Msg{From: ev.from, Frame: ev.frame, Payload: ev.payload})
	g.afterSwarmMsg()
}

// handleProgress 处理经信令回退上报的块位图（GROUP_PROGRESS）。
func (g *groupTask) handleProgress(ev groupEvent) {
	var p protocol.GroupProgressPayload
	if ev.env == nil || ev.env.DecodePayload(&p) != nil {
		return
	}
	if _, ok := g.cand[ev.from]; !ok {
		g.cand[ev.from] = nil
	}
	g.bm[ev.from] = p.HaveBitmap
	g.rebuild()
}

// handleDown 某成员块连接断开。Swarm 引擎在每次块帧时自行通过 sink 上抛
// 矩阵，连接断开仅是瞬态（引擎会在重连/重建后继续），故此处无需额外动作。
func (g *groupTask) handleDown(_ groupEvent) {}

// rebuild 依据当前名册重建 Swarm，并重放各成员最近位图恢复状态。
func (g *groupTask) rebuild() {
	members := g.memberIDs()
	sink := groupSink{g: g}
	if g.source {
		g.swarm = group.NewSource(g.groupID, g.id, g.cli.selfID, g.src, members, g.tr, sink)
	} else {
		participants := append(append([]string{}, members...), g.peerName, g.cli.selfID)
		g.swarm = group.NewReceiver(g.groupID, g.id, g.cli.selfID, g.src, participants, []string{g.peerName}, g.tr, sink)
	}

	// 恢复进度（顺序重要）：
	// 1) 先把本机已落盘的块以 BLOCK 帧重放（From 记源）。重建产生的新
	//    swarm self.have 为空；WriteBlock 幂等（重复块直接 no-op），引擎
	//    据此置位 self.have，使本机只请求真正缺失的块，不重写也不误判完成。
	// 2) 再重放各成员真实位图，覆盖第 1 步给源临时写入的本地位图。
	if !g.source {
		bc := g.blockCount()
		for idx := 0; idx < bc; idx++ {
			off := idx / 64 * 8
			if binary.BigEndian.Uint64(g.src.got[off:])&(1<<uint(idx%64)) == 0 {
				continue
			}
			g.swarm.HandleMsg(group.Msg{From: g.peerName, Frame: protocol.FrameBlock, Payload: u32BE(idx)})
		}
	}
	for id, raw := range g.bm {
		if len(raw) == 0 {
			continue
		}
		g.swarm.HandleMsg(group.Msg{From: id, Frame: protocol.FrameBitfield, Payload: bitmapPayload(g.blockCount(), raw)})
	}
	g.swarm.Start()
}

// afterSwarmMsg 在每收到一条块帧后更新进度/完成回调。
func (g *groupTask) afterSwarmMsg() {
	if g.source {
		g.emitSourceProgress()
		return
	}
	g.emitReceiverProgress()
}

// emitSourceProgress 依据各成员位图统计累计已交付字节并更新任务行。
//
// 完成集合只增不减：接收方在 swarm 启动时会广播一次（可能延迟的）空位图，
// 若它在收齐后的满位图之后到达，不能因此把已完成成员退回未完成。
func (g *groupTask) emitSourceProgress() {
	var delivered int64
	total := len(g.cand)
	for id, raw := range g.bm {
		n := bitmapCount(g.blockCount(), raw)
		delivered += int64(n) * group.BlockSize
		if n >= g.blockCount() {
			g.doneIDs[id] = true
		}
	}
	done := len(g.doneIDs)
	if total > 0 && delivered > g.size*int64(total) {
		delivered = g.size * int64(total)
	}
	cp := g.progressSnapshot(delivered, 0)
	g.cache(cp)
	g.cli.listener().OnTransferProgress(cp)
	// 成员矩阵由 Swarm 引擎在块帧推进时经 sink 上抛，此处不重复。
	if done == total && total > 0 && !g.completed {
		g.completed = true
		g.finish()
	}
}

// emitReceiverProgress 按已落盘块数更新本机接收进度，收齐则完成。
func (g *groupTask) emitReceiverProgress() {
	have := g.src.writtenBlocks()
	bytesDone := int64(have) * group.BlockSize
	if bytesDone > g.size {
		bytesDone = g.size
	}
	cp := g.progressSnapshot(bytesDone, 0)
	g.cache(cp)
	g.cli.listener().OnTransferProgress(cp)
	g.cli.listener().OnGroupMatrix(g.groupID, g.id, g.sourceBitmap())
	if have >= g.blockCount() && !g.completed {
		if err := g.src.finalizeErr(); err != nil {
			g.completed = true
			g.fail(err)
			return
		}
		g.completed = true
		g.finish()
	}
}

// fail 上抛失败回调并清理任务。
func (g *groupTask) fail(err error) {
	g.cli.listener().OnTransferFailed(g.id, err.Error())
	g.cli.removeGroupTask(g.id)
	g.teardown()
}

func (g *groupTask) finish() {
	cp := g.progressSnapshot(g.size, 0)
	cp.State = appapi.TransferDone
	cp.BytesDone = g.size
	g.cache(cp)
	g.cli.listener().OnTransferProgress(cp)
	g.cli.listener().OnTransferDone(g.id)
	g.cli.removeGroupTask(g.id)
	g.teardown()
}

// progressSnapshot 构造对齐 appapi.Transfer 的快照。
func (g *groupTask) progressSnapshot(bytesDone, speed int64) appapi.Transfer {
	return appapi.Transfer{
		ID:        g.id,
		Direction: g.direction(),
		State:     appapi.TransferActive,
		Kind:      g.kind(),
		GroupID:   g.groupID,
		PeerID:    g.peerName,
		Name:      g.name,
		Size:      g.size,
		BytesDone: bytesDone,
		SpeedBps:  speed,
	}
}

func (g *groupTask) direction() appapi.TransferDirection {
	if g.source {
		return appapi.TransferOutbound
	}
	return appapi.TransferInbound
}

func (g *groupTask) kind() appapi.PathKind {
	if g.groupID == protocol.GroupBroadcast {
		return appapi.PathSwarm
	}
	return appapi.PathChannel
}

// emitPending 缓存并上抛初始 pending 进度，让 UI 建立任务行。
func (g *groupTask) emitPending() {
	cp := g.progressSnapshot(0, 0)
	g.cache(cp)
	g.cli.listener().OnTransferProgress(cp)
}

func (g *groupTask) blockCount() int { return blockCount(g.size) }

// currentSnapshot 返回 actor 最近缓存的进度快照（供 GetState，无并发 map 访问）。
func (g *groupTask) currentSnapshot() appapi.Transfer {
	g.snapMu.Lock()
	defer g.snapMu.Unlock()
	return g.cached
}

// cache 记录最新快照。
func (g *groupTask) cache(t appapi.Transfer) {
	g.snapMu.Lock()
	g.cached = t
	g.snapMu.Unlock()
}

// memberIDs 返回已加入成员（源视角），排序保证确定性。
func (g *groupTask) memberIDs() []string {
	out := make([]string, 0, len(g.cand))
	for id := range g.cand {
		out = append(out, id)
	}
	return out
}

// teardown 停止传输与 actor，并从客户端注册表移除。
func (g *groupTask) teardown() {
	if g.tr != nil {
		g.tr.closeAll()
	}
	if g.src != nil {
		g.src.close()
	}
	g.stop()
}

// ---- 位图小工具（名册里存的是原始位图字节）----

func bitmapPayload(blocks int, raw []byte) []byte {
	// 重放给 Swarm 的 BITFIELD payload 需带 blockCount 前缀。
	return append(u32BE(blocks), raw...)
}

func bitmapCount(blocks int, raw []byte) int {
	n := countBitmapBits(raw)
	if n > blocks {
		return blocks
	}
	return n
}

func (g *groupTask) sourceBitmap() []byte {
	if g.source {
		// 源持有全部块。
		return fullBitmap(g.blockCount())
	}
	return g.srcBitmapRaw()
}
