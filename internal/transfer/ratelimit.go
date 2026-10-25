package transfer

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	bwLimitBps atomic.Int64
	bwMu       sync.Mutex
	bwNextSlot time.Time
)

func init() {
	if raw := strings.TrimSpace(os.Getenv("LANCHAT_BWLIMIT_MBPS")); raw != "" {
		if mb, err := strconv.ParseFloat(raw, 64); err == nil && mb > 0 {
			SetBandwidthLimitBps(int64(mb * 1024 * 1024))
		}
	}
}

// SetBandwidthLimitBps 设置全局文件传输带宽上限（字节/秒，<=0 表示不限速，M21）。
func SetBandwidthLimitBps(bps int64) {
	if bps < 0 {
		bps = 0
	}
	bwLimitBps.Store(bps)
	bwMu.Lock()
	bwNextSlot = time.Time{}
	bwMu.Unlock()
}

// BandwidthLimitBps 返回当前全局文件传输带宽上限（字节/秒，0 表示不限速）。
func BandwidthLimitBps() int64 {
	return bwLimitBps.Load()
}

// throttleBytes 按全局带宽上限对 n 字节传输做平滑节流；未开启限速时零开销返回。
func throttleBytes(ctx context.Context, n int) error {
	limit := bwLimitBps.Load()
	if limit <= 0 || n <= 0 {
		return nil
	}
	dur := time.Duration(float64(n) / float64(limit) * float64(time.Second))
	if dur <= 0 {
		return nil
	}

	bwMu.Lock()
	now := time.Now()
	if bwNextSlot.Before(now) {
		bwNextSlot = now
	}
	wait := bwNextSlot.Sub(now)
	bwNextSlot = bwNextSlot.Add(dur)
	bwMu.Unlock()

	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
