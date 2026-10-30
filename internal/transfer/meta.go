package transfer

import (
	"encoding/json"
	"os"
	"time"
)

// metaState 是 DestPath.meta 的内容，记录断点续传所需最小状态。
// v8.2.0 新增 ChunkBitmap 支持多路径乱序 Chunk 写入的位图级精准断点续传。
type metaState struct {
	TransferID  string `json:"transferID"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ChunkSize   int    `json:"chunkSize"`
	BytesDone   int64  `json:"bytesDone"`
	ChunkBitmap []byte `json:"chunkBitmap,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
}

func (m *metaState) ensureBitmap(totalChunks int) {
	if totalChunks <= 0 {
		return
	}
	needBytes := (totalChunks + 7) / 8
	if len(m.ChunkBitmap) < needBytes {
		nb := make([]byte, needBytes)
		copy(nb, m.ChunkBitmap)
		m.ChunkBitmap = nb
	}
	if m.ChunkSize <= 0 {
		m.ChunkSize = ChunkSize
	}
	// 将连续前缀 BytesDone 已覆盖的完整 Chunk 同步置位。
	contiguousChunks := int(m.BytesDone / int64(m.ChunkSize))
	if m.Size > 0 && m.BytesDone >= m.Size {
		contiguousChunks = totalChunks
	}
	for i := 0; i < contiguousChunks && i < totalChunks; i++ {
		m.ChunkBitmap[i/8] |= 1 << uint(i%8)
	}
}

func (m *metaState) isChunkDone(idx int) bool {
	if idx < 0 || idx/8 >= len(m.ChunkBitmap) {
		return false
	}
	return (m.ChunkBitmap[idx/8] & (1 << uint(idx%8))) != 0
}

func (m *metaState) markChunkDone(idx, totalChunks int, totalSize int64) {
	m.ensureBitmap(totalChunks)
	if idx >= 0 && idx < totalChunks {
		m.ChunkBitmap[idx/8] |= 1 << uint(idx%8)
	}
	// 更新连续前缀 BytesDone（兼容单流回退续传），全部置位时设为 totalSize。
	contiguous := 0
	for contiguous < totalChunks && m.isChunkDone(contiguous) {
		contiguous++
	}
	if contiguous >= totalChunks {
		m.BytesDone = totalSize
		return
	}
	off := int64(contiguous) * int64(ChunkSize)
	if off > totalSize {
		off = totalSize
	}
	if off > m.BytesDone {
		m.BytesDone = off
	}
}

func (m *metaState) completedChunksAndBytes(totalChunks int, totalSize int64) (int, int64) {
	m.ensureBitmap(totalChunks)
	var cnt int
	var bytes int64
	for i := 0; i < totalChunks; i++ {
		if m.isChunkDone(i) {
			cnt++
			cStart := int64(i) * int64(ChunkSize)
			cEnd := cStart + int64(ChunkSize)
			if cEnd > totalSize {
				cEnd = totalSize
			}
			if cEnd > cStart {
				bytes += cEnd - cStart
			}
		}
	}
	return cnt, bytes
}

// loadMeta 读取 meta 文件；不存在时返回 (nil, nil)。
func loadMeta(path string) (*metaState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m metaState
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// saveMeta 原子写入 meta：先写临时文件再 rename，保证崩溃时不半写。
func saveMeta(path string, m *metaState) error {
	m.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// metaWriter 在字节数推进时节流落盘 meta（每达到 step 字节，或距上次落盘
// 超过 interval），避免每个 ACK 都同步写盘。
type metaWriter struct {
	path     string
	state    *metaState
	step     int64
	interval time.Duration

	lastSaved  int64
	lastSaveAt time.Time
}

func newMetaWriter(path string, state *metaState, step int64, interval time.Duration) *metaWriter {
	return &metaWriter{
		path:       path,
		state:      state,
		step:       step,
		interval:   interval,
		lastSaved:  state.BytesDone,
		lastSaveAt: time.Now(),
	}
}

// update 把 bytesDone 写入状态，并按节流策略落盘；force 立即落盘。
func (w *metaWriter) update(bytesDone int64, force bool) error {
	w.state.BytesDone = bytesDone
	now := time.Now()
	if !force && bytesDone-w.lastSaved < w.step && now.Sub(w.lastSaveAt) < w.interval {
		return nil
	}
	if err := saveMeta(w.path, w.state); err != nil {
		return err
	}
	w.lastSaved = bytesDone
	w.lastSaveAt = now
	return nil
}

// flush 立即落盘当前状态。
func (w *metaWriter) flush() error {
	return w.update(w.state.BytesDone, true)
}
