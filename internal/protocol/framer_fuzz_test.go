package protocol

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// readOnlyWriter 让只读字节流也满足 io.ReadWriter；fuzz 只调 ReadFrame，
// 绝不调用 Write。
type readOnlyWriter struct{ io.Reader }

func (readOnlyWriter) Write(p []byte) (int, error) { panic("write on read-only fuzz stream") }

// FuzzDecodeFrame 向 DecodeFrame 投喂任意字节，要求永不 panic：
// 坏魔数 / 坏版本 / 声明超大 length / 截断等都必须走错误分支。
func FuzzDecodeFrame(f *testing.F) {
	f.Add([]byte("LCTP"))
	f.Add([]byte("LCTP\x01\x08\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte("XXXX\x01\x08\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := DecodeFrame(data)
		if err != nil {
			return
		}
		// 解析成功则重新编码必须还原成同样的字节。
		re, err := frame.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if !bytes.Equal(re, data) {
			t.Fatalf("round trip changed bytes")
		}
	})
}

// FuzzFramer 把随机字节流喂给 Framer.ReadFrame：
// 模拟真实 TCP 任意粘包/拆包，要求永不 panic、不会因 length 字段耗尽内存。
func FuzzFramer(f *testing.F) {
	f.Add([]byte("LCTP\x01\x08\x00\x00\x00\x00\x00\x00\x03"))
	f.Add([]byte{0x01})
	f.Add([]byte("garbage stream with random length ffffffffff"))
	f.Add([]byte("LCTP\x01\x01\x00\x00\x00\x00\x00\x05hello"))

	f.Fuzz(func(t *testing.T, stream []byte) {
		fr := NewFramer(readOnlyWriter{bytes.NewReader(stream)})
		// 字节流有界，最多读到流干；坏魔数等正常错误直接结束。
		for {
			frame, err := fr.ReadFrame()
			if err != nil {
				if frame != nil {
					t.Fatal("frame must be nil on error")
				}
				return
			}
			if len(frame.Payload) > MaxFramePayload {
				t.Fatal("payload exceeded cap")
			}
		}
	})
}

// FuzzFrameRoundTrip 从 fuzz 输入构造若干帧，编码后按随机切点分段喂给
// Framer，解出的帧必须与原帧逐字段一致。
func FuzzFrameRoundTrip(f *testing.F) {
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{})
	f.Add([]byte{0x08, 0x00, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		want, encoded, ok := buildFrames(data)
		if !ok {
			return
		}
		if len(want) == 0 {
			return
		}
		// 用 data 自身派生切分点把 encoded 切段灌入 buffer。
		var buf bytes.Buffer
		pos, i := 0, 0
		for pos < len(encoded) {
			step := 1 + int(data[i%len(data)]&0x7)
			end := pos + step
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.Write(encoded[pos:end])
			pos = end
			i++
		}

		fr := NewFramer(readOnlyWriter{&buf})
		for _, w := range want {
			got, err := fr.ReadFrame()
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got.Type != w.Type || got.Flags != w.Flags || !bytes.Equal(got.Payload, w.Payload) {
				t.Fatalf("frame mismatch")
			}
		}
	})
}

// buildFrames 把 fuzz 输入解析为帧序列及其编码字节。
// 输入编码：[type, flagsHi, flagsLo, lenB3, lenB2, lenB1, lenB0, payload...]，可重复。
func buildFrames(data []byte) ([]Frame, []byte, bool) {
	var frames []Frame
	var encoded []byte
	for len(data) >= 7 {
		length := int(uint32(data[3])<<24 | uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6]))
		if length > 1024 { // fuzz 里限制单帧大小，避免 corpus 膨胀
			return nil, nil, false
		}
		if len(data) < 7+length {
			return nil, nil, false
		}
		fr := Frame{
			Type:  data[0],
			Flags: binary.BigEndian.Uint16(data[1:3]),
		}
		if length > 0 {
			fr.Payload = append([]byte(nil), data[7:7+length]...)
		}
		raw, err := fr.Encode()
		if err != nil {
			return nil, nil, false
		}
		frames = append(frames, fr)
		encoded = append(encoded, raw...)
		data = data[7+length:]
	}
	return frames, encoded, true
}
