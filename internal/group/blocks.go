package group

import (
	"encoding/binary"
	"errors"
)

// BlockSize 复用 transfer 的 4 MiB 块大小（方案 §9.3）。
const BlockSize = 4 << 20

// 块交换信令帧的 payload 编解码错误。
var (
	errShortBlockPayload = errors.New("group: short block payload")
	errBlockIndex        = errors.New("group: block index out of range")
)

// blockIdxSize REQUEST/HAVE 帧内块下标的字节长度。
const blockIdxSize = 4

// encodeBlockIdx 编码 REQUEST/HAVE：blockIdx（4B 大端）。
func encodeBlockIdx(idx int) []byte {
	buf := make([]byte, blockIdxSize)
	binary.BigEndian.PutUint32(buf, uint32(idx))
	return buf
}

// decodeBlockIdx 解码 REQUEST/HAVE 的块下标。
func decodeBlockIdx(payload []byte) (int, error) {
	if len(payload) < blockIdxSize {
		return 0, errShortBlockPayload
	}
	return int(binary.BigEndian.Uint32(payload[:blockIdxSize])), nil
}

// encodeBlock 编码 BLOCK 帧：blockIdx（4B 大端）+ 块数据。
func encodeBlock(idx int, data []byte) []byte {
	buf := make([]byte, blockIdxSize+len(data))
	binary.BigEndian.PutUint32(buf[:blockIdxSize], uint32(idx))
	copy(buf[blockIdxSize:], data)
	return buf
}

// decodeBlock 解码 BLOCK 帧，返回块下标与数据副本。
func decodeBlock(payload []byte) (int, []byte, error) {
	if len(payload) < blockIdxSize {
		return 0, nil, errShortBlockPayload
	}
	idx := int(binary.BigEndian.Uint32(payload[:blockIdxSize]))
	data := make([]byte, len(payload)-blockIdxSize)
	copy(data, payload[blockIdxSize:])
	return idx, data, nil
}

// encodeBitfield 编码 BITFIELD 帧：blockCount（4B 大端）+ bitmap。
func encodeBitfield(blockCount int, bm *bitmap) []byte {
	raw := bm.bytes()
	buf := make([]byte, blockIdxSize+len(raw))
	binary.BigEndian.PutUint32(buf[:blockIdxSize], uint32(blockCount))
	copy(buf[blockIdxSize:], raw)
	return buf
}

// decodeBitfield 解码 BITFIELD 帧，返回块数与位图。
func decodeBitfield(payload []byte) (int, *bitmap, error) {
	if len(payload) < blockIdxSize {
		return 0, nil, errShortBlockPayload
	}
	blockCount := int(binary.BigEndian.Uint32(payload[:blockIdxSize]))
	return blockCount, loadBitmap(payload[blockIdxSize:]), nil
}
