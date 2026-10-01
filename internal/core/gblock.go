package core

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/pandaymx/lanchat/internal/group"
)

// blockCount 计算文件按 4 MiB 分块的块数（至少 1 块）。
func blockCount(size int64) int {
	n := int((size + group.BlockSize - 1) / group.BlockSize)
	if n < 1 {
		return 1
	}
	return n
}

// fileBlockSource 是 group.BlockSource 的生产实现。
//
// 源（source）打开原始文件只读，按块供数据；接收方把乱序到达的块写入
// 预截断的 .part 文件，收齐全部块后做全文件 SHA-256 校验并 rename 为
// 目标文件。本 PR 不做跨重启的块级续传（崩溃后重新分发）。
type fileBlockSource struct {
	mu sync.Mutex

	f *os.File

	blocks int
	size   int64

	// 仅接收方使用：
	wantSHA string
	dest    string
	written int    // 不同块的计数（块写一次性，去重由引擎保证）
	got     []byte // 已落盘块位图（与 written 同步）
	part    string

	// finalErr 记录收齐后校验/落盘结果，供 actor 决定成功或失败上抛。
	finalErr error

	closed bool // 文件是否已关闭（finalize 与 close 互斥，防二次 Close）
}

// newSourceBlockSource 打开源文件（只读）。
func newSourceBlockSource(f *os.File, size int64) *fileBlockSource {
	return &fileBlockSource{f: f, blocks: blockCount(size), size: size}
}

// newReceiverBlockSource 创建预截断的 .part 文件。
func newReceiverBlockSource(dest string, size int64, wantSHA string) (*fileBlockSource, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if size > 0 {
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	bc := blockCount(size)
	return &fileBlockSource{
		f: f, blocks: bc, size: size,
		wantSHA: wantSHA, dest: dest, part: part,
		got: make([]byte, ((bc+63)/64)*8),
	}, nil
}

func (s *fileBlockSource) BlockCount() int { return s.blocks }

// ReadBlock 读取第 idx 块（最后一块可短于 BlockSize）。
func (s *fileBlockSource) ReadBlock(idx int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= s.blocks {
		return nil, errors.New("core: block index out of range")
	}
	off := int64(idx) * group.BlockSize
	n := group.BlockSize
	if rem := s.size - off; rem < int64(n) {
		n = int(rem)
	}
	buf := make([]byte, n)
	if _, err := s.f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteBlock 落盘第 idx 块；收齐后做全文件校验并 rename。幂等：重复写同一
// 块（rebuild 后对端可能重发）直接忽略，完成与否以块位图为准而非写次数。
func (s *fileBlockSource) WriteBlock(idx int, data []byte) error {
	s.mu.Lock()
	if idx < 0 || idx >= s.blocks {
		s.mu.Unlock()
		return errors.New("core: block index out of range")
	}
	wordOff := idx / 64 * 8
	if binary.BigEndian.Uint64(s.got[wordOff:])&(1<<uint(idx%64)) != 0 {
		s.mu.Unlock()
		return nil // 该块已落盘，忽略重发
	}
	off := int64(idx) * group.BlockSize
	if _, err := s.f.WriteAt(data, off); err != nil {
		s.mu.Unlock()
		return err
	}
	// 位图按大端 uint64 字序列化（与 group BITFIELD / OnGroupMatrix 一致）。
	word := s.got[wordOff : wordOff+8]
	binary.BigEndian.PutUint64(word, binary.BigEndian.Uint64(word)|1<<uint(idx%64))
	s.written = countBitmapBits(s.got)
	if s.written == s.blocks {
		s.finalErr = s.finalizeLocked()
	}
	s.mu.Unlock()
	return nil
}

// finalizeErr 返回收齐后的校验/落盘结果（未收齐时为 nil）。
func (s *fileBlockSource) finalizeErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalErr
}

// finalizeLocked 校验全文件 SHA-256 并 rename 为目标文件。调用方须持有 s.mu。
func (s *fileBlockSource) finalizeLocked() error {
	f := s.f

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != s.wantSHA {
		return errChecksum
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.closed = true
	if err := os.Rename(s.part, s.dest); err != nil {
		return err
	}
	return nil
}

// close 关闭底层文件（取消或异常时调用），可重复调用。
func (s *fileBlockSource) close() {
	s.mu.Lock()
	if !s.closed {
		_ = s.f.Close()
		s.closed = true
	}
	s.mu.Unlock()
}
