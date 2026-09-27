package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// Framer 在一条流式连接（TCP / Unix socket）上按 LCTP 帧做定长分包，
// 自动处理粘包与拆包。
//
// 同一连接上通常只会有一个 goroutine 读、一个 goroutine 写；
// 为兼容多 goroutine 场景，写路径加了互斥保护。
type Framer struct {
	r io.Reader
	w io.Writer

	headerBuf [FrameHeaderSize]byte

	writeMu sync.Mutex
}

// NewFramer 基于已建立的双向连接构造 Framer。
func NewFramer(rw io.ReadWriter) *Framer {
	return &Framer{r: rw, w: rw}
}

// ReadFrame 阻塞读取并解析一个完整帧。
// 对端正常关闭且一个字节都没读到时返回 io.EOF；读到半截则返回错误。
func (fr *Framer) ReadFrame() (*Frame, error) {
	if _, err := io.ReadFull(fr.r, fr.headerBuf[:]); err != nil {
		return nil, err
	}
	hdr := fr.headerBuf

	var magic [4]byte
	copy(magic[:], hdr[0:4])
	if magic != LCTPMagic {
		return nil, ErrBadMagic
	}
	if hdr[4] != LCTPVersion {
		return nil, ErrUnsupportedVer
	}
	length := binary.BigEndian.Uint32(hdr[8:12])
	if length > MaxFramePayload {
		return nil, ErrFrameTooLarge
	}

	f := &Frame{
		Type:  hdr[5],
		Flags: binary.BigEndian.Uint16(hdr[6:8]),
	}
	if length > 0 {
		f.Payload = make([]byte, length)
		if _, err := io.ReadFull(fr.r, f.Payload); err != nil {
			if errors.Is(err, io.EOF) {
				// 对端在 payload 中途关闭：语义不够明确，包装为 ErrUnexpectedEOF。
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
	return f, nil
}

// WriteFrame 写入一个完整帧（帧头与 payload 各写一次，调用方需保证
// 底层连接不与其他写者直接共享）。
func (fr *Framer) WriteFrame(f *Frame) error {
	if len(f.Payload) > MaxFramePayload {
		return ErrFrameTooLarge
	}

	fr.writeMu.Lock()
	defer fr.writeMu.Unlock()

	var hdr [FrameHeaderSize]byte
	copy(hdr[0:4], LCTPMagic[:])
	hdr[4] = LCTPVersion
	hdr[5] = f.Type
	binary.BigEndian.PutUint16(hdr[6:8], f.Flags)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(f.Payload)))

	if _, err := fr.w.Write(hdr[:]); err != nil {
		return err
	}
	if len(f.Payload) > 0 {
		if _, err := fr.w.Write(f.Payload); err != nil {
			return err
		}
	}
	return nil
}
