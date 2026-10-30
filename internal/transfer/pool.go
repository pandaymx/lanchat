package transfer

import "sync"

const (
	smallPoolTier  = 64 * 1024
	mediumPoolTier = 256 * 1024
	chunkPoolTier  = ChunkSize + dataHeaderSize
)

var (
	smallBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, smallPoolTier)
			return &b
		},
	}
	mediumBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, mediumPoolTier)
			return &b
		},
	}
	chunkBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, chunkPoolTier)
			return &b
		},
	}
)

// AcquireChunkBuffer 从分级 sync.Pool 缓冲池获取长度为 size 的字节切片（v8.2.0），
// 消除高吞吐单流/多路径并发传输中的高频 1 MiB 堆分配与 GC 压力。
func AcquireChunkBuffer(size int) []byte {
	if size <= 0 {
		return nil
	}
	switch {
	case size <= smallPoolTier:
		bp := smallBufPool.Get().(*[]byte)
		return (*bp)[:size]
	case size <= mediumPoolTier:
		bp := mediumBufPool.Get().(*[]byte)
		return (*bp)[:size]
	case size <= chunkPoolTier:
		bp := chunkBufPool.Get().(*[]byte)
		return (*bp)[:size]
	default:
		return make([]byte, size)
	}
}

// ReleaseChunkBuffer 将 AcquireChunkBuffer 获取的切片归还至对应级别的 sync.Pool（v8.2.0）。
func ReleaseChunkBuffer(buf []byte) {
	c := cap(buf)
	switch c {
	case smallPoolTier:
		full := buf[:smallPoolTier]
		smallBufPool.Put(&full)
	case mediumPoolTier:
		full := buf[:mediumPoolTier]
		mediumBufPool.Put(&full)
	case chunkPoolTier:
		full := buf[:chunkPoolTier]
		chunkBufPool.Put(&full)
	}
}
