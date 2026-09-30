package group

import (
	"encoding/binary"
	"math/bits"
)

// bitmap 是块拥有情况的紧凑位图：第 i 位为 1 表示持有第 i 个块。
// 底层为小端位序，按 uint64 字增长，长度与 BlockCount 解耦（可为空）。
type bitmap struct {
	bits []uint64
}

// newBitmap 按块数分配位图。
func newBitmap(blockCount int) *bitmap {
	if blockCount <= 0 {
		return &bitmap{}
	}
	return &bitmap{bits: make([]uint64, (blockCount+63)/64)}
}

func (b *bitmap) set(i int) {
	if i < 0 {
		return
	}
	w := i / 64
	if w >= len(b.bits) {
		grown := make([]uint64, w+1)
		copy(grown, b.bits)
		b.bits = grown
	}
	b.bits[w] |= 1 << uint(i%64)
}

func (b *bitmap) has(i int) bool {
	if i < 0 || i/64 >= len(b.bits) {
		return false
	}
	return b.bits[i/64]&(1<<uint(i%64)) != 0
}

// count 返回被置位的总数。
func (b *bitmap) count() int {
	n := 0
	for _, w := range b.bits {
		n += bits.OnesCount64(w)
	}
	return n
}

// missing 返回 [0,blockCount) 内所有未置位的块下标。
func (b *bitmap) missing(blockCount int) []int {
	var out []int
	for i := 0; i < blockCount; i++ {
		if !b.has(i) {
			out = append(out, i)
		}
	}
	return out
}

// bytes 序列化为大端紧凑字节串（对齐 GROUP_PROGRESS.haveBitmap 与
// BITFIELD 帧的 bitmap 部分）。
func (b *bitmap) bytes() []byte {
	out := make([]byte, len(b.bits)*8)
	for i, w := range b.bits {
		binary.BigEndian.PutUint64(out[i*8:], w)
	}
	return out
}

// loadBitmap 从字节串恢复位图。
func loadBitmap(data []byte) *bitmap {
	bm := &bitmap{bits: make([]uint64, len(data)/8)}
	for i := range bm.bits {
		bm.bits[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	return bm
}
