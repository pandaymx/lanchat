package hubstate

import (
	"sync"
	"testing"
)

func TestSequencerStartsAfterStart(t *testing.T) {
	s := NewSequencer(10)
	if got := s.Last(); got != 10 {
		t.Fatalf("初始 Last 应为 10，实际 %d", got)
	}
	if got := s.Next(); got != 11 {
		t.Fatalf("首次 Next 应为 11，实际 %d", got)
	}
	if got := s.Next(); got != 12 {
		t.Fatalf("第二次 Next 应为 12，实际 %d", got)
	}
	if got := s.Last(); got != 12 {
		t.Fatalf("两次 Next 后 Last 应为 12，实际 %d", got)
	}
}

func TestSequencerZeroStart(t *testing.T) {
	s := NewSequencer(0)
	if got := s.Next(); got != 1 {
		t.Fatalf("从 0 起首次 Next 应为 1，实际 %d", got)
	}
}

// TestSequencerConcurrentUnique 验证并发分配不重复。
// 这是同步正确性的底线：出现重复序号会让客户端漏消息。
func TestSequencerConcurrentUnique(t *testing.T) {
	const (
		goroutines = 16
		perG       = 500
	)
	s := NewSequencer(0)

	var wg sync.WaitGroup
	seen := make([][]uint64, goroutines)
	for i := range goroutines {
		seen[i] = make([]uint64, 0, perG)
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for range perG {
				seen[idx] = append(seen[idx], s.Next())
			}
		}(i)
	}
	wg.Wait()

	want := uint64(goroutines * perG)
	if got := s.Last(); got != want {
		t.Fatalf("Last 应为 %d，实际 %d", want, got)
	}

	uniq := make(map[uint64]struct{}, want)
	for _, list := range seen {
		for _, v := range list {
			if _, dup := uniq[v]; dup {
				t.Fatalf("序号 %d 被分配了两次", v)
			}
			uniq[v] = struct{}{}
		}
	}
	if uint64(len(uniq)) != want {
		t.Fatalf("唯一序号应有 %d 个，实际 %d", want, len(uniq))
	}
}
