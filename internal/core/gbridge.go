package core

import (
	"encoding/binary"
	"math/bits"

	"github.com/pandaymx/lanchat/internal/group"
)

// groupSink 实现 group.EventSink：Swarm 的矩阵事件由此上抛给 UI。
type groupSink struct{ g *groupTask }

func (s groupSink) OnMatrix(ev group.MatrixEvent) {
	s.g.cli.listener().OnGroupMatrix(ev.GroupID, ev.TransferID, ev.HaveBitmap)
}

func (s groupSink) OnChannel(_ group.ChannelEvent) {}

// ---- groupTask 注册表 ----

func (c *Client) addGroupTask(t *groupTask) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.groupTasks == nil {
		c.groupTasks = map[string]*groupTask{}
	}
	if _, ok := c.groupTasks[t.id]; ok {
		return false
	}
	c.groupTasks[t.id] = t
	return true
}

func (c *Client) getGroupTask(id string) (*groupTask, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.groupTasks[id]
	return t, ok
}

func (c *Client) removeGroupTask(id string) {
	c.mu.Lock()
	delete(c.groupTasks, id)
	c.mu.Unlock()
}

// ---- BlockSource 进度读取 ----

// writtenBlocks 返回接收方已落盘的不同块数。
func (s *fileBlockSource) writtenBlocks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// ---- 位图小工具 ----

func u32BE(v int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

// countBitmapBits 统计大端 uint64 字位图中的置位块数。
func countBitmapBits(raw []byte) int {
	n := 0
	for i := 0; i+8 <= len(raw); i += 8 {
		n += bits.OnesCount64(binary.BigEndian.Uint64(raw[i:]))
	}
	return n
}

// fullBitmap 返回块 i ∈ [0,blocks) 全部置位的大端 uint64 字位图。
func fullBitmap(blocks int) []byte {
	// group 位图按 8 字节字、大端 uint64 序列化；块 i 置位到其字的 bit i%64。
	raw := make([]byte, ((blocks+63)/64)*8)
	for i := 0; i < blocks; i++ {
		off := i / 64 * 8
		binary.BigEndian.PutUint64(raw[off:], binary.BigEndian.Uint64(raw[off:])|1<<uint(i%64))
	}
	return raw
}

// srcBitmapRaw 返回接收方当前块位图原始字节。
func (g *groupTask) srcBitmapRaw() []byte {
	src := g.src
	src.mu.Lock()
	defer src.mu.Unlock()
	return append([]byte(nil), src.got...)
}

// 类型别名，便于 gtransport 引用 group.Msg。
type groupMsg = group.Msg
