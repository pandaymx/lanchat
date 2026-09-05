package hubstate

import (
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// msg 构造一条测试消息，只填 Query 关心的字段。
func msg(conv string, seq uint64) protocol.StoredMessage {
	return protocol.StoredMessage{
		ID:             "m",
		ConversationID: conv,
		ServerSeq:      seq,
		Body:           "body",
	}
}

func TestHistoryQueryAfter(t *testing.T) {
	h := NewHistory()
	for i := uint64(1); i <= 5; i++ {
		h.Append(msg("c1", i))
	}

	resp := h.Query("c1", 0, 0)
	if len(resp.Messages) != 5 {
		t.Fatalf("after=0 应返回 5 条，实际 %d", len(resp.Messages))
	}
	if resp.Messages[0].ServerSeq != 1 || resp.Messages[4].ServerSeq != 5 {
		t.Fatalf("返回顺序应为 1..5，实际首 %d 尾 %d",
			resp.Messages[0].ServerSeq, resp.Messages[4].ServerSeq)
	}
	if resp.HasMore {
		t.Error("已取完不应有 HasMore")
	}

	// after=3 应返回 4,5 —— 严格大于，不含 3
	resp = h.Query("c1", 3, 0)
	if len(resp.Messages) != 2 {
		t.Fatalf("after=3 应返回 2 条，实际 %d", len(resp.Messages))
	}
	if resp.Messages[0].ServerSeq != 4 {
		t.Fatalf("首条应为 4，实际 %d", resp.Messages[0].ServerSeq)
	}
}

func TestHistoryQueryLimitAndHasMore(t *testing.T) {
	h := NewHistory()
	for i := uint64(1); i <= 10; i++ {
		h.Append(msg("c1", i))
	}

	resp := h.Query("c1", 0, 3)
	if len(resp.Messages) != 3 {
		t.Fatalf("limit=3 应返回 3 条，实际 %d", len(resp.Messages))
	}
	if !resp.HasMore {
		t.Error("还剩 7 条时 HasMore 应为 true")
	}

	// 续传：用最后一条的 seq 作为新的 after
	last := resp.Messages[2].ServerSeq
	resp2 := h.Query("c1", last, 3)
	if len(resp2.Messages) != 3 {
		t.Fatalf("续传应返回 3 条，实际 %d", len(resp2.Messages))
	}
	if !resp2.HasMore {
		t.Error("还剩 4 条时 HasMore 应为 true")
	}
}

func TestHistoryQueryEmptyBoundary(t *testing.T) {
	h := NewHistory()
	for i := uint64(1); i <= 3; i++ {
		h.Append(msg("c1", i))
	}

	// after 超过最大值 → 空且无更多
	resp := h.Query("c1", 99, 0)
	if len(resp.Messages) != 0 {
		t.Fatalf("应返回空，实际 %d", len(resp.Messages))
	}
	if resp.HasMore {
		t.Error("无更多时 HasMore 应为 false")
	}

	// 不存在的会话
	resp = h.Query("nope", 0, 0)
	if len(resp.Messages) != 0 {
		t.Fatalf("不存在的会话应返回空，实际 %d", len(resp.Messages))
	}
}

// TestHistoryCrossConversation 验证跨会话查询按全局 ServerSeq 排序。
// 这是「客户端首次全量拉取」的路径，顺序必须是全局序而非会话内序。
func TestHistoryCrossConversation(t *testing.T) {
	h := NewHistory()
	// 交错写入两个会话，制造「按会话分桶后顺序被打乱」的局面
	h.Append(msg("c1", 1))
	h.Append(msg("c2", 2))
	h.Append(msg("c1", 3))
	h.Append(msg("c2", 4))
	h.Append(msg("c1", 5))

	resp := h.Query("", 0, 0)
	if len(resp.Messages) != 5 {
		t.Fatalf("跨会话应返回 5 条，实际 %d", len(resp.Messages))
	}
	for i, m := range resp.Messages {
		if m.ServerSeq != uint64(i+1) {
			t.Fatalf("第 %d 条 seq 应为 %d，实际 %d", i, i+1, m.ServerSeq)
		}
	}

	// 跨会话也能 after 过滤
	resp = h.Query("", 3, 0)
	if len(resp.Messages) != 2 {
		t.Fatalf("after=3 跨会话应返回 2 条，实际 %d", len(resp.Messages))
	}
	if resp.Messages[0].ServerSeq != 4 {
		t.Fatalf("首条应为 4，实际 %d", resp.Messages[0].ServerSeq)
	}
}

// TestHistoryReturnsCopy 验证返回的是副本而非内部数组。
// 若返回引用，调用方序列化期间另一条消息 Append 进来会触发 data race。
func TestHistoryReturnsCopy(t *testing.T) {
	h := NewHistory()
	h.Append(msg("c1", 1))

	resp := h.Query("c1", 0, 0)
	resp.Messages[0].Body = "mutated"

	if got := h.Query("c1", 0, 0).Messages[0].Body; got != "body" {
		t.Fatalf("修改返回值不应影响内部状态，实际 %q", got)
	}
}

func TestHistoryMaxSeq(t *testing.T) {
	h := NewHistory()
	if got := h.MaxSeq(); got != 0 {
		t.Fatalf("空 History 的 MaxSeq 应为 0，实际 %d", got)
	}

	h.Append(msg("c1", 7))
	h.Append(msg("c2", 3))
	h.Append(msg("c1", 12))
	if got := h.MaxSeq(); got != 12 {
		t.Fatalf("MaxSeq 应为 12，实际 %d", got)
	}
}

// TestHistoryBounded 验证单会话缓存有上界，不会无限膨胀。
func TestHistoryBounded(t *testing.T) {
	h := NewHistory()
	total := maxHistoryPerConv + 1000
	for i := uint64(1); i <= uint64(total); i++ {
		h.Append(msg("c1", i))
	}

	if got := h.Len("c1"); got > maxHistoryPerConv {
		t.Fatalf("缓存应被裁剪到 <= %d，实际 %d", maxHistoryPerConv, got)
	}

	// 裁剪后仍保持有序且是最新的那批
	resp := h.Query("c1", 0, 0)
	for i := 1; i < len(resp.Messages); i++ {
		if resp.Messages[i-1].ServerSeq >= resp.Messages[i].ServerSeq {
			t.Fatalf("裁剪后顺序被破坏：%d >= %d",
				resp.Messages[i-1].ServerSeq, resp.Messages[i].ServerSeq)
		}
	}
	if last := resp.Messages[len(resp.Messages)-1].ServerSeq; last != uint64(total) {
		t.Fatalf("应保留最新的消息，末尾 seq 应为 %d，实际 %d", total, last)
	}
}

func TestHistoryReset(t *testing.T) {
	h := NewHistory()
	h.Append(msg("c1", 1))
	h.Reset()
	if h.Len("c1") != 0 {
		t.Error("Reset 后应为空")
	}
	if h.MaxSeq() != 0 {
		t.Error("Reset 后 MaxSeq 应为 0")
	}
}
