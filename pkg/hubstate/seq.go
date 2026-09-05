package hubstate

import "sync/atomic"

// Sequencer 是 Hub 端的 ServerSeq 单调分配器。
//
// ServerSeq 是整套同步机制的地基：
//   - 消息排序以它为准，不依赖任何客户端时钟（见 AGENTS.md 架构铁律 §3.4）
//   - 客户端的离线补发起点是「我已收到的最大 ServerSeq」
//   - per-device 读游标存的也是它（ADR-008）
//
// 因此它必须**严格单调递增且并发安全**，用 atomic 而非 mutex：
// 分配在消息热路径上，mutex 会成为不必要的瓶颈。
type Sequencer struct {
	n atomic.Uint64
}

// NewSequencer 构造一个从 start 开始的分配器。
// 首次 Next() 返回 start+1。
//
// start 的用途是恢复：Hub 重启后从 Store 里已落库的最大 ServerSeq 起步，
// 避免重启后新消息与历史消息序号撞车（那会让客户端的补发游标彻底错乱）。
func NewSequencer(start uint64) *Sequencer {
	s := &Sequencer{}
	s.n.Store(start)
	return s
}

// Next 返回下一个序号。并发安全。
func (s *Sequencer) Next() uint64 { return s.n.Add(1) }

// Last 返回已分配的最大序号。从未分配过时返回构造时的 start。
//
// 注意它**不是**「当前最大值」的强一致读——并发 Next 期间读到的是某个瞬时值。
// 这只用于观测与测试断言，不用于分配决策。
func (s *Sequencer) Last() uint64 { return s.n.Load() }
