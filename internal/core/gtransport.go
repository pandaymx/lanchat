package core

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/internal/discover"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// groupHello 是群组块连接上首个 LCTP HELLO 帧的 JSON 负载。
//
// 数据面帧不携带成员标识，故握手时显式声明 memberID，并用一次性 token 证明
// 自己确属本次分发（token 由源在 GROUP_OFFER 中下发，全体成员共用）。
type groupHello struct {
	MemberID string `json:"memberID"`
	Token    string `json:"token"`
}

// blockConn 是一条到单个成员的块交换 TCP 连接及其帧器。
type blockConn struct {
	fr     *protocol.Framer
	closer func() error
}

func (bc blockConn) close() { _ = bc.closer() }

// maxPendingPerPeer 限制单个成员在连接建立前缓冲的待发帧数，防止异常情况下
// 内存无限增长（引擎每成员在途请求唯一，正常队列很短）。
const maxPendingPerPeer = 64

// blockTransport 实现 group.Transport：按 To 选取到该成员的块 TCP 连接，
// BITFIELD/REQUEST/BLOCK/HAVE 均经独立数据面连接承载，字节永不进 WebSocket。
//
// 为避免两端同时拨号产生交叉双连接，连接归属由双方成员 ID 确定性裁决：ID
// 较大者作为唯一拨号方，较小者仅监听。连接建立前要发出的帧进入该成员的
// pending 队列，连接就绪后统一冲刷；非拨号方的帧随对端拨入一并送出。
// 全部方法并发安全。
type blockTransport struct {
	selfID string
	// selfToken 是本机签发的握手 token，用于校验入站连接（对端必须出示它）。
	selfToken string

	mu    sync.Mutex
	peers map[string][]string // memberID -> 候选 host:port
	// peerTokens[member] 是 member 签发的 token，拨号连接 member 时本机出示它。
	peerTokens map[string]string
	conns      map[string]blockConn
	listen     net.Listener
	dialing    map[string]bool
	pending    map[string][][]byte
	closed     bool

	// onIncoming 把对端经块连接发来的帧交给上层（groupTask actor）。
	onIncoming func(from string, frame byte, payload []byte)
	// onPeerDown 通知上层某成员块连接彻底失败（标记 failed）。
	onPeerDown func(peer string)
}

func newBlockTransport(selfID, selfToken string, incoming func(string, byte, []byte), down func(string)) *blockTransport {
	return &blockTransport{
		selfID:     selfID,
		selfToken:  selfToken,
		peers:      map[string][]string{},
		peerTokens: map[string]string{},
		conns:      map[string]blockConn{},
		dialing:    map[string]bool{},
		pending:    map[string][][]byte{},
		onIncoming: incoming,
		onPeerDown: down,
	}
}

// shouldDial 是连接裁决：仅当本机 ID 大于对端时才由本机拨号，否则只等对方拨入。
func (t *blockTransport) shouldDial(peer string) bool {
	return t.selfID > peer
}

// listenOn 在通配地址上监听块连接，供其他成员拨入；返回本机候选地址。
func (t *blockTransport) listenOn() ([]string, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.listen = ln
	t.mu.Unlock()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	go t.acceptLoop(ln)
	portNum, err := strconv.Atoi(port)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return discover.CandidateAddrs(portNum), nil
}

func (t *blockTransport) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go t.serveInbound(c)
	}
}

// serveInbound 处理一条对端主动拨入的连接：读握手、验 token，再按 ID 裁决
// 决定保留还是关闭，最后持续读帧。
func (t *blockTransport) serveInbound(c net.Conn) {
	hello, err := readGroupHello(c)
	if err != nil || hello.Token != t.selfToken {
		_ = c.Close()
		return
	}
	// 连接裁决：只有 ID 较大的一方应拨号。对端若不该拨号（其 ID 更小），
	// 说明它违反裁决，关闭此连接——正确方向的连接应由本机发起。
	if hello.MemberID <= t.selfID {
		_ = c.Close()
		return
	}
	if err := writeGroupHello(c, t.selfID, t.selfToken); err != nil {
		_ = c.Close()
		return
	}
	t.attach(hello.MemberID, c)
}

// attach 把已完成握手的连接登记为到 member 的唯一连接、冲刷待发帧并启动读泵。
func (t *blockTransport) attach(member string, c net.Conn) {
	t.mu.Lock()
	if old, ok := t.conns[member]; ok {
		t.mu.Unlock()
		old.close()
		t.mu.Lock()
	}
	bc := blockConn{fr: protocol.NewFramer(c), closer: c.Close}
	t.conns[member] = bc
	queue := compactPending(t.pending[member])
	t.pending[member] = nil
	t.mu.Unlock()

	for _, p := range queue {
		_ = bc.fr.WriteFrame(&protocol.Frame{Type: p[0], Payload: p[1:]})
	}
	t.readLoop(member, bc)
}

// compactPending 压缩待发队列：BITFIELD 是对全部块持有情况的快照，只有最新
// 一条有意义，故丢弃被后续 BITFIELD 取代的旧位图（避免陈旧空位图延迟覆盖
// 对端已完成状态）；REQUEST/BLOCK/HAVE 保持原顺序。
func compactPending(queue [][]byte) [][]byte {
	lastBitfield := -1
	for i, p := range queue {
		if p[0] == protocol.FrameBitfield {
			lastBitfield = i
		}
	}
	if lastBitfield <= 0 {
		// 没有 BITFIELD，或唯一一条就在最前：无需压缩。
		return queue
	}
	out := make([][]byte, 0, len(queue))
	for i, p := range queue {
		if p[0] == protocol.FrameBitfield && i != lastBitfield {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (t *blockTransport) readLoop(member string, bc blockConn) {
	for {
		f, err := bc.fr.ReadFrame()
		if err != nil {
			bc.close()
			t.dropConn(member, bc)
			t.handleDisconnect(member)
			return
		}
		if t.onIncoming != nil {
			t.onIncoming(member, f.Type, f.Payload)
		}
	}
}

// handleDisconnect 在连接断开后通知上层；若本机是该成员的裁决拨号方，则
// 延迟重连（无待发帧也保持连通），避免帧流永久中断。
func (t *blockTransport) handleDisconnect(member string) {
	if t.onPeerDown != nil {
		t.onPeerDown(member)
	}
	t.mu.Lock()
	closed := t.closed
	_, connected := t.conns[member]
	dialing := t.dialing[member]
	shouldDial := t.shouldDial(member)
	if !closed && !connected && !dialing && shouldDial {
		t.dialing[member] = true
	} else {
		shouldDial = false
	}
	cand := append([]string(nil), t.peers[member]...)
	tok := t.peerTokens[member]
	t.mu.Unlock()
	if shouldDial {
		go t.redial(member, cand, tok)
	}
}

// dropConn 仅在当前登记连接仍是 bc 时移除，避免误删后来重连的新连接。
func (t *blockTransport) dropConn(member string, bc blockConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cur, ok := t.conns[member]; ok && cur.fr == bc.fr {
		delete(t.conns, member)
	}
}

// setCandidates 登记/更新某成员的块候选地址。
func (t *blockTransport) setCandidates(member string, cand []string) {
	t.mu.Lock()
	t.peers[member] = cand
	t.mu.Unlock()
}

// setPeerToken 登记某成员签发的握手 token。
func (t *blockTransport) setPeerToken(member, token string) {
	t.mu.Lock()
	t.peerTokens[member] = token
	t.mu.Unlock()
}

// Send 实现 group.Transport：连接已建立则直接写；否则把帧并入 pending 队列，
// 并在本机为裁决拨号方时惰性发起连接，帧在连接就绪后冲刷。
func (t *blockTransport) Send(m groupMsg) {
	t.mu.Lock()
	if bc, ok := t.conns[m.To]; ok {
		t.mu.Unlock()
		_ = bc.fr.WriteFrame(&protocol.Frame{Type: m.Frame, Payload: m.Payload})
		return
	}
	if q := len(t.pending[m.To]); q < maxPendingPerPeer {
		t.pending[m.To] = append(t.pending[m.To], append([]byte{m.Frame}, m.Payload...))
	}
	if !t.dialing[m.To] && t.shouldDial(m.To) {
		t.dialing[m.To] = true
		startDial := true
		cand := append([]string(nil), t.peers[m.To]...)
		tok := t.peerTokens[m.To]
		t.mu.Unlock()
		if startDial {
			go t.dial(m.To, cand, tok)
		}
		return
	}
	t.mu.Unlock()
}

// dial 尝试连接 member 并完成握手，成功后交给 attach；失败清空 dialing 标志，
// 由后续 Send 或重连逻辑再次尝试。
func (t *blockTransport) dial(member string, cand []string, peerToken string) {
	c := dialCandidates(context.Background(), cand)
	if c == nil {
		t.clearDialing(member)
		t.notifyDown(member)
		return
	}
	if err := writeGroupHello(c, t.selfID, peerToken); err != nil {
		_ = c.Close()
		t.clearDialing(member)
		t.notifyDown(member)
		return
	}
	hello, err := readGroupHello(c)
	if err != nil || hello.MemberID != member {
		_ = c.Close()
		t.clearDialing(member)
		t.notifyDown(member)
		return
	}
	t.clearDialing(member)
	t.attach(member, c)
}

// redial 由连接断开触发：先做短退避，再按普通拨号流程重连。
func (t *blockTransport) redial(member string, cand []string, peerToken string) {
	timer := time.NewTimer(groupRedialBackoff)
	<-timer.C
	t.dial(member, cand, peerToken)
}

func (t *blockTransport) clearDialing(member string) {
	t.mu.Lock()
	delete(t.dialing, member)
	t.mu.Unlock()
}

func (t *blockTransport) notifyDown(member string) {
	if t.onPeerDown != nil {
		t.onPeerDown(member)
	}
}

// closeAll 关闭监听与全部块连接。
func (t *blockTransport) closeAll() {
	t.mu.Lock()
	t.closed = true
	ln := t.listen
	conns := make([]blockConn, 0, len(t.conns))
	for _, bc := range t.conns {
		conns = append(conns, bc)
	}
	t.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, bc := range conns {
		bc.close()
	}
}

const (
	groupDialTimeout   = 5 * time.Second
	groupRedialBackoff = 2 * time.Second
)

// dialCandidates 按错峰方式并发尝试候选，返回首个连接成功的连接。
func dialCandidates(ctx context.Context, candidates []string) net.Conn {
	if len(candidates) == 0 {
		return nil
	}
	type result struct {
		c   net.Conn
		idx int
	}
	resCh := make(chan result, len(candidates))
	for i, addr := range candidates {
		go func(idx int, address string) {
			d := net.Dialer{Timeout: groupDialTimeout}
			c, err := d.DialContext(ctx, "tcp", address)
			if err == nil {
				resCh <- result{c: c, idx: idx}
			}
		}(i, addr)
	}
	winner := <-resCh
	// 关闭其余晚到连接：等待一轮，简单起见不做精细收集。
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for n := len(candidates) - 1; n > 0; n-- {
			select {
			case r := <-resCh:
				_ = r.c.Close()
			case <-timer.C:
				return
			}
		}
	}()
	return winner.c
}

func writeGroupHello(c net.Conn, memberID, token string) error {
	data, err := json.Marshal(groupHello{MemberID: memberID, Token: token})
	if err != nil {
		return err
	}
	return protocol.NewFramer(c).WriteFrame(&protocol.Frame{Type: protocol.FrameHello, Payload: data})
}

func readGroupHello(c net.Conn) (groupHello, error) {
	f, err := protocol.NewFramer(c).ReadFrame()
	if err != nil || f.Type != protocol.FrameHello {
		return groupHello{}, errBadGroupHello
	}
	var h groupHello
	if err := json.Unmarshal(f.Payload, &h); err != nil {
		return groupHello{}, err
	}
	return h, nil
}
