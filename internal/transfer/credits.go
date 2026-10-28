package transfer

import "time"

// 信用窗口与分片大小（方案 §5.3/§15）。
const (
	// MinCreditBytes 初始窗口大小 1 MiB。
	MinCreditBytes = 1 << 20
	// MaxCreditBytes 在途字节上限 4 MiB。
	MaxCreditBytes = 4 << 20
	// ChunkSize 每次 DATA 携带 1 MiB。
	ChunkSize = 1 << 20
)

// RecommendStreamCount 根据探测到的链路 RTT 与文件大小自适应推荐并发 TCP 流数（阶段三 v6.3.0）：
//   - 小文件（< 4 MiB）：单流直传，避免多流建连开销；
//   - 物理局域网低延迟（RTT <= 2ms）：中文件 2 流，大文件（>= 32 MiB）4 流；
//   - Tailscale / 跨网段中延迟（2ms < RTT <= 25ms）：中文件 4 流，大文件 6 流以充分填满 BDP 管道；
//   - 高延迟跨地域隧道（RTT > 25ms）：大文件扩展至 8 流。
func RecommendStreamCount(rtt time.Duration, fileSize int64) int {
	if fileSize < 4<<20 {
		return 1
	}
	large := fileSize >= 32<<20
	switch {
	case rtt <= 2*time.Millisecond:
		if large {
			return 4
		}
		return 2
	case rtt <= 25*time.Millisecond:
		if large {
			return 6
		}
		return 4
	default:
		if large {
			return 8
		}
		return 4
	}
}

// AdaptiveCreditWindow 根据实时 RTT 与吞吐率估算带宽时延积（2 * BDP），
// 在 [MinCreditBytes, MaxCreditBytes] 区间内动态计算最优接收信用窗口（阶段三 v6.3.0）。
func AdaptiveCreditWindow(rtt time.Duration, throughputBps int64) int64 {
	if rtt <= 0 || throughputBps <= 0 {
		return MinCreditBytes
	}
	bdp := int64(float64(throughputBps) * rtt.Seconds() * 2.0)
	if bdp < MinCreditBytes {
		return MinCreditBytes
	}
	if bdp > MaxCreditBytes {
		return MaxCreditBytes
	}
	return bdp
}

// creditWindow 维护发送侧「已发送未确认」字节窗口。
//
// 状态以累计字节计：
//   - sent：已经发送到的偏移（含在途）；
//   - acked：接收侧累计确认到的偏移。
//
// 接收侧每个 ACK 携带目标窗口大小 window，发送侧允许发送到 acked+window。
type creditWindow struct {
	sent   int64
	acked  int64
	window int64
}

// newCreditWindow 创建窗口，初始目标窗口为 initial。
func newCreditWindow(initial int64) *creditWindow {
	return &creditWindow{window: initial}
}

// limit 返回当前允许发送到的累计偏移。
func (w *creditWindow) limit() int64 {
	return w.acked + w.window
}

// available 返回当前还能发送的字节数。
func (w *creditWindow) available() int64 {
	avail := w.limit() - w.sent
	if avail < 0 {
		return 0
	}
	return avail
}

// acquire 在窗口额度内取本次可发字节，返回不超过 n 的可取数量（可能为 0）。
func (w *creditWindow) acquire(n int) int {
	if n <= 0 {
		return 0
	}
	avail := w.available()
	if avail <= 0 {
		return 0
	}
	take := int64(n)
	if take > avail {
		take = avail
	}
	w.sent += take
	return int(take)
}

// grant 据 ACK 更新累计确认点与目标窗口大小。
//
//   - cumOffset：接收侧累计确认到的偏移；
//   - window：接收侧声明的新目标窗口大小（封顶 MaxCreditBytes）。
func (w *creditWindow) grant(cumOffset int64, window int64) {
	if cumOffset > w.acked {
		w.acked = cumOffset
	}
	if w.acked > w.sent {
		// 确认点不应超过已发送点；防御性收敛。
		w.acked = w.sent
	}
	if window < 0 {
		window = 0
	}
	if window > MaxCreditBytes {
		window = MaxCreditBytes
	}
	w.window = window
}
