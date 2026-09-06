package hubstate

import (
	"sort"
	"sync"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// History 是已投递消息的**内存冗余备份**，用于离线补发。
//
// 为什么不用 Store 直接查历史：
// Store 是通用持久层，查询要满足各种业务口径；而补发是一个极高频、
// 模式固定的操作（「给我 seq > N 的、按序、最多 M 条」）。
// 这里用「按会话分桶 + 每桶按 ServerSeq 升序」的切片，
// 二分定位 + 顺序切片的复杂度是 O(log n + M)，比走一遍 SQL 便宜得多。
//
// 内存是有界的：见 maxHistoryPerConv。聊天场景下补发只需要最近的消息，
// 从头到尾全量重放既没人用得起内存，客户端也不需要。
//
// 它不替代 Store —— Store 负责持久化（重启后还在），History 只服务在线期间的补发。
// 二者关系见 router.go：写路径是「先 Store 后 History」，读路径优先走 History。
type History struct {
	mu sync.RWMutex

	// buckets[convID] = 按 ServerSeq 升序排列的消息。
	// 约定：Append 时调用方保证 ServerSeq 递增（由 Sequencer 分配），
	// 因此桶内天然有序，不需要每次插入都排序。
	buckets map[string][]protocol.StoredMessage
}

// maxHistoryPerConv 是单个会话保留的最大消息条数。
//
// 超出后丢弃最旧的。理由：补发只需要覆盖「客户端离线期间漏掉的量」，
// 一个客户端不可能离线到需要重放 5000 条（那应该走全量 Store 拉取）。
// 这里定 5000 是给压力测试和异常长会话留的余量，正常场景远用不到。
const maxHistoryPerConv = 5000

// HistoryRestoreLimit 是 Hub 重启后从持久化 Store 灌回内存补发缓冲的
// 条数上限，与 maxHistoryPerConv（单会话缓冲容量）对齐——恢复量超过
// 缓冲容量没有意义，超出部分仍在 Store 里，客户端可整页刷新拉全量。
const HistoryRestoreLimit = maxHistoryPerConv

// NewHistory 构造一个空的补发缓冲。
func NewHistory() *History {
	return &History{buckets: make(map[string][]protocol.StoredMessage)}
}

// Append 记录一条已分配 ServerSeq 的消息。
//
// 调用方必须**按顺序**追加（ServerSeq 递增）。如果乱序追加，
// 桶内不再有序，二分定位会给出错误结果——这是调用契约，不是运行时能兜住的错。
// Router 的写路径天然满足这一点（seq 由同一个 Sequencer 现分配现追加）。
func (h *History) Append(m protocol.StoredMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	b := h.buckets[m.ConversationID]
	b = append(b, m)

	// 超限时丢弃最旧的一批（一次性砍到 90%，避免每来一条都做一次 copy）
	if len(b) > maxHistoryPerConv {
		drop := len(b) - maxHistoryPerConv + maxHistoryPerConv/10
		if drop > len(b) {
			drop = len(b)
		}
		b = append([]protocol.StoredMessage(nil), b[drop:]...)
	}
	h.buckets[m.ConversationID] = b
}

// Query 返回历史消息，按 ServerSeq 升序，最多 limit 条。
//
// 分页方向（before 优先）：
//   - before>0：向更早翻页，返回 ServerSeq 严格小于 before 的最晚一批；
//     HasMore=true 表示 before 之前还有更老的消息，客户端用本批第一条
//     的 ServerSeq 作为新的 before 继续翻。
//   - before==0 且 after>0：增量补发，返回 ServerSeq 严格大于 after 的最早一批；
//     HasMore=true 时用本批最后一条的 ServerSeq 作为新的 after 续传。
//   - 两者都为 0：从最老的消息开始。
//
// 其余参数语义：
//   - convID 为空表示跨所有会话查询（客户端首次全量拉取）
//   - limit <= 0 表示不限条数（调用方负责设上限，见 Router）
func (h *History) Query(convID string, after, before uint64, limit int) protocol.HistoryResponse {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var list []protocol.StoredMessage
	if convID != "" {
		list = h.buckets[convID]
	} else {
		// 跨会话：合并后按 ServerSeq 排序。
		// 这里必须排序，因为各桶之间是独立的序列。
		total := 0
		for _, b := range h.buckets {
			total += len(b)
		}
		if total == 0 {
			return protocol.HistoryResponse{}
		}
		list = make([]protocol.StoredMessage, 0, total)
		for _, b := range h.buckets {
			list = append(list, b...)
		}
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].ServerSeq < list[j].ServerSeq
		})
	}

	// 向更早翻页：hi 是第一个 ServerSeq >= before 的下标，[0,hi) 即全部更老消息。
	// 取其中最晚的 limit 条（窗口 [lo,hi)），返回仍是升序。
	if before > 0 {
		hi := sort.Search(len(list), func(i int) bool { return list[i].ServerSeq >= before })
		lo := 0
		if limit > 0 && hi-limit > 0 {
			lo = hi - limit
		}
		if lo >= hi {
			return protocol.HistoryResponse{Messages: nil, HasMore: false}
		}
		out := make([]protocol.StoredMessage, hi-lo)
		copy(out, list[lo:hi])
		// 窗口前还有消息 → 还能继续往更老翻。
		return protocol.HistoryResponse{Messages: out, HasMore: lo > 0}
	}

	// 二分找第一个 ServerSeq > after 的下标
	lo := sort.Search(len(list), func(i int) bool { return list[i].ServerSeq > after })
	end := len(list)
	if limit > 0 && lo+limit < end {
		end = lo + limit
	}
	if lo >= end {
		return protocol.HistoryResponse{Messages: nil, HasMore: false}
	}

	// 返回副本：调用方会把它 JSON 序列化后发出去，
	// 给切片本身会让调用方持有内部数组的引用（并发读下有 data race 风险）。
	out := make([]protocol.StoredMessage, end-lo)
	copy(out, list[lo:end])
	return protocol.HistoryResponse{
		Messages: out,
		HasMore:  end < len(list),
	}
}

// Len 返回某会话当前缓存的消息条数，用于测试与观测。
// convID 为空时无意义（返回 0）。
func (h *History) Len(convID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.buckets[convID])
}

// MaxSeq 返回已缓存的最大 ServerSeq；无消息时返回 0。
//
// 用途是 Hub 启动时的灾后恢复：把 Sequencer 的起点抬到这个值之上，
// 避免重启后新消息的序号与缓存里的历史撞车。
func (h *History) MaxSeq() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var highest uint64
	for _, b := range h.buckets {
		if n := len(b); n > 0 {
			if s := b[n-1].ServerSeq; s > highest {
				highest = s
			}
		}
	}
	return highest
}

// Reset 清空所有缓存。
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buckets = make(map[string][]protocol.StoredMessage)
}
