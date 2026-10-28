package transfer

import "testing"

// TestCreditWindowInitial 验证初始窗口可用额度等于初始授信。
func TestCreditWindowInitial(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)
	if got := w.available(); got != MinCreditBytes {
		t.Fatalf("初始可用 = %d，期望 %d", got, MinCreditBytes)
	}
}

// TestCreditAcquireNotExceed 验证 acquire 不超过请求量与窗口。
func TestCreditAcquireNotExceed(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)

	// 请求小于窗口。
	if got := w.acquire(100); got != 100 {
		t.Fatalf("acquire = %d，期望 100", got)
	}
	if got := w.available(); got != MinCreditBytes-100 {
		t.Fatalf("剩余 = %d，期望 %d", got, MinCreditBytes-100)
	}

	// 请求大于剩余，只取剩余。
	got := w.acquire(MinCreditBytes)
	if got != MinCreditBytes-100 {
		t.Fatalf("acquire = %d，期望 %d（封顶剩余）", got, MinCreditBytes-100)
	}
	if w.available() != 0 {
		t.Fatal("窗口耗尽后期望 0")
	}
	if w.acquire(10) != 0 {
		t.Fatal("无可用信用时 acquire 应为 0")
	}
}

// TestCreditGrantMovesAckPoint 验证 grant 推进确认点并释放新窗口。
func TestCreditGrantMovesAckPoint(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)
	_ = w.acquire(MinCreditBytes) // 发满 1 MiB，在途 1 MiB
	if w.available() != 0 {
		t.Fatal("发满后应无可用额度")
	}

	// ACK 确认 1 MiB，目标窗口放大到 MaxCreditBytes。
	w.grant(MinCreditBytes, MaxCreditBytes)
	if w.acked != MinCreditBytes {
		t.Fatalf("acked = %d，期望 %d", w.acked, MinCreditBytes)
	}
	// 新 limit = acked + window = 1MiB + 4MiB；sent = 1MiB，故可用 4MiB。
	if got := w.available(); got != MaxCreditBytes {
		t.Fatalf("grant 后可用 = %d，期望 %d", got, MaxCreditBytes)
	}
}

// TestCreditGrantWindowCap 验证窗口大小封顶 MaxCreditBytes。
func TestCreditGrantWindowCap(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)
	w.grant(0, MaxCreditBytes+1000)
	if w.window != MaxCreditBytes {
		t.Fatalf("window = %d，期望封顶 %d", w.window, MaxCreditBytes)
	}
}

// TestCreditGrantAckNotExceedSent 验证确认点不能超过已发送点。
func TestCreditGrantAckNotExceedSent(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)
	w.grant(MinCreditBytes+500, MinCreditBytes) // sent=0，ack 不应越过
	if w.acked != 0 {
		t.Fatalf("acked = %d，期望被收敛到 0", w.acked)
	}
}

// TestCreditAcquireNonPositive 验证非正请求返回 0。
func TestCreditAcquireNonPositive(t *testing.T) {
	w := newCreditWindow(MinCreditBytes)
	if w.acquire(0) != 0 || w.acquire(-1) != 0 {
		t.Fatal("非正请求应返回 0")
	}
}

func TestRecommendStreamCountAndAdaptiveCreditWindow(t *testing.T) {
	if n := RecommendStreamCount(500_000, 1<<20); n != 1 {
		t.Fatalf("small file stream count = %d, want 1", n)
	}
	if n := RecommendStreamCount(1_000_000, 64<<20); n != 4 {
		t.Fatalf("LAN large file stream count = %d, want 4", n)
	}
	if n := RecommendStreamCount(12_000_000, 64<<20); n != 6 {
		t.Fatalf("Tailscale medium-RTT large file stream count = %d, want 6", n)
	}
	if n := RecommendStreamCount(45_000_000, 64<<20); n != 8 {
		t.Fatalf("high-RTT large file stream count = %d, want 8", n)
	}
	if win := AdaptiveCreditWindow(0, 0); win != MinCreditBytes {
		t.Fatalf("AdaptiveCreditWindow(0, 0) = %d, want %d", win, MinCreditBytes)
	}
	if win := AdaptiveCreditWindow(20_000_000, 100<<20); win != MaxCreditBytes {
		t.Fatalf("AdaptiveCreditWindow(20ms, 100MB/s) = %d, want cap %d", win, MaxCreditBytes)
	}
}
