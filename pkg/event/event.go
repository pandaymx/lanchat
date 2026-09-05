// Package event 提供 core.EventBus 的进程内 fan-out 实现。
//
// 设计取舍：
//   - 慢订阅者只会丢自己的事件，**不会阻塞其它订阅者**。
//     当 channel 缓冲满时，Publish 直接 dropped（并递增 droppedCount）；
//     可靠性由 Store 和 Conn 重传保证，EventBus 不背锅。
//   - 用 sync.Mutex 而非 atomic，是为了 debug 时能在 panic 拿到全部订阅者的快照。
package event

import (
	"sync"

	"github.com/pandaymx/lanchat/pkg/core"
)

// New 返回一个新的进程内 EventBus。
func New() core.EventBus {
	return &localBus{
		subs: make(map[int]*sub),
		next: 1,
	}
}

type localBus struct {
	mu   sync.Mutex
	subs map[int]*sub
	next int
}

// sub 是单个订阅者。
//
// 字段含义：
//   - alive 由 bus.mu 保护；Publish 快照取走后即使 alive 变 false 也只是
//     之后的下一次 Publish 不再投递，本轮投递已经拿到指针；
//   - closeOnce 用于确保 Close 幂等；
//   - 删除该 sub 必须在 bus.mu 下完成（避免 Close 后仍被 Publish 看到）。
type sub struct {
	parent *localBus
	id     int
	buf    int
	ch     chan core.Event

	mu        sync.Mutex
	alive     bool
	closeOnce sync.Once
}

// Publish 把事件扇出给所有当前订阅者。任意订阅者的 channel 满则丢它的事件。
//
// 同步策略：
//   - 进入临界区后取所有 alive==true 的 sub 指针快照；
//   - 退出临界区后向每个 sub 投递——慢订阅者不能阻塞其他订阅者。
//   - 由于 alive 同时被 bus.mu 保护，Copy in 阶段不会被 Close 修改；
//     之后再读 alive 时改用 sub.mu 防止 data race。
func (b *localBus) Publish(e core.Event) {
	b.mu.Lock()
	snapshot := make([]*sub, 0, len(b.subs))
	for _, s := range b.subs {
		if s.alive {
			snapshot = append(snapshot, s)
		}
	}
	b.mu.Unlock()

	for _, s := range snapshot {
		s.mu.Lock()
		stillAlive := s.alive
		s.mu.Unlock()
		if !stillAlive {
			continue
		}
		select {
		case s.ch <- e:
		default:
			// dropped. 不重试、不告警。
		}
	}
}

// Subscribe 返回一条新的订阅。buf 是每订阅者 channel 缓冲大小。
func (b *localBus) Subscribe(buf int) core.Subscription {
	if buf <= 0 {
		buf = 16
	}
	b.mu.Lock()
	id := b.next
	b.next++
	s := &sub{
		parent: b,
		id:     id,
		buf:    buf,
		ch:     make(chan core.Event, buf),
		alive:  true,
	}
	s.mu.Lock()
	b.subs[id] = s
	s.mu.Unlock()
	b.mu.Unlock()

	return s
}

// C 返回只读 channel。
func (s *sub) C() <-chan core.Event { return s.ch }

// Close 取消订阅（之后 Publish 不会再投递）。重复关闭幂等。
//
// 加 bus.mu 是为了与 Publish 互斥：本函数在临界区内把自己从 subs 里删除，
// 防止 Publish 拿到指针之后另一边的 Close 并发修改 alive。
func (s *sub) Close() error {
	s.closeOnce.Do(func() {
		s.parent.mu.Lock()
		defer s.parent.mu.Unlock()
		s.mu.Lock()
		s.alive = false
		s.mu.Unlock()
		delete(s.parent.subs, s.id)
	})
	return nil
}
