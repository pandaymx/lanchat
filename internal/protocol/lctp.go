// Package protocol 中 lctp.go 定义 P2P 数据面二进制帧（LANChat Transfer Protocol）。
//
// 帧格式（大端序，头部固定 12 字节）：
//
//	0        1        2        3        4        5        6        7        8
//	├────────┼────────┼────────┼────────┼────────┼────────┼────────┼────────┤
//	│ 'L'  'C'  'T'  'P' │ ver │ type │       flags       │  length (u32)   │
//	├────────┴────────┴────────┴────────┴────────┴────────┴────────┴────────┤
//	│                        payload[length]                                │
//
// 本文件只负责帧的结构与字节级编解码；具体传输语义（握手 / 信用窗口 /
// 续传 / 群组块交换）在 transfer、group 包实现。
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FrameHeaderSize LCTP 固定帧头长度（字节）。
const FrameHeaderSize = 12

// LCTPMagic LCTP 帧魔数。
var LCTPMagic = [4]byte{'L', 'C', 'T', 'P'}

// LCTPVersion 当前数据面帧版本。
const LCTPVersion byte = 1

// MaxFramePayload 单帧 payload 上限：block 4 MiB 加少量帧内字段余量。
// 超长帧一律拒绝，防止对端声明巨大 length 撑爆内存。
const MaxFramePayload = 4<<20 + 64

// 数据面帧类型（对齐方案 §5.2）。
const (
	FrameHello    byte = 0x01 // 握手：token, resumeOffset
	FrameAccept   byte = 0x02 // 握手应答：size, chunkSize, blockSize
	FrameData     byte = 0x03 // 单播数据：offset + bytes
	FrameAck      byte = 0x04 // 确认：cumOffset, credit
	FrameFin      byte = 0x05 // 结束：sha256, totalBytes
	FrameError    byte = 0x06 // 错误
	FrameCancel   byte = 0x07 // 取消
	FramePing     byte = 0x08 // 保活
	FrameBitfield byte = 0x10 // 群组：blockCount + bitmap
	FrameRequest  byte = 0x11 // 群组：请求块
	FrameBlock    byte = 0x12 // 群组：块数据
	FrameHave     byte = 0x13 // 群组：通告新块
)

// 帧级错误。
var (
	ErrBadMagic       = errors.New("lctp: bad magic")
	ErrUnsupportedVer = errors.New("lctp: unsupported version")
	ErrFrameTooLarge  = errors.New("lctp: frame payload too large")
)

// Frame 表示一个已解析的 LCTP 帧。
type Frame struct {
	Type    byte
	Flags   uint16
	Payload []byte
}

// Encode 将帧序列化为「帧头 + payload」字节串。
func (f *Frame) Encode() ([]byte, error) {
	if len(f.Payload) > MaxFramePayload {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, FrameHeaderSize+len(f.Payload))
	copy(buf[0:4], LCTPMagic[:])
	buf[4] = LCTPVersion
	buf[5] = f.Type
	binary.BigEndian.PutUint16(buf[6:8], f.Flags)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(f.Payload)))
	copy(buf[FrameHeaderSize:], f.Payload)
	return buf, nil
}

// DecodeFrame 从一个完整帧的字节串解析出 Frame。
// 调用方通常不需要直接使用；流式场景请用 Framer.ReadFrame。
func DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < FrameHeaderSize {
		return nil, fmt.Errorf("lctp: short frame: %d bytes", len(data))
	}
	var magic [4]byte
	copy(magic[:], data[0:4])
	if magic != LCTPMagic {
		return nil, ErrBadMagic
	}
	if data[4] != LCTPVersion {
		return nil, fmt.Errorf("%w: got %d", ErrUnsupportedVer, data[4])
	}
	length := binary.BigEndian.Uint32(data[8:12])
	if length > MaxFramePayload {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, length)
	}
	if len(data) != FrameHeaderSize+int(length) {
		return nil, fmt.Errorf("lctp: length field %d does not match %d payload bytes", length, len(data)-FrameHeaderSize)
	}
	f := &Frame{
		Type:  data[5],
		Flags: binary.BigEndian.Uint16(data[6:8]),
	}
	if length > 0 {
		f.Payload = make([]byte, length)
		copy(f.Payload, data[FrameHeaderSize:])
	}
	return f, nil
}
