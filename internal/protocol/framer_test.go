package protocol

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
)

func TestFrameEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Frame{
		{Type: FramePing, Flags: 0, Payload: nil},
		{Type: FrameHello, Flags: 1, Payload: []byte("token-resume")},
		{Type: FrameBlock, Flags: 0xFFFF, Payload: make([]byte, 4096)},
	}
	for i, want := range cases {
		data, err := want.Encode()
		if err != nil {
			t.Fatalf("case %d Encode: %v", i, err)
		}
		if len(data) != FrameHeaderSize+len(want.Payload) {
			t.Fatalf("case %d encoded size = %d", i, len(data))
		}
		got, err := DecodeFrame(data)
		if err != nil {
			t.Fatalf("case %d DecodeFrame: %v", i, err)
		}
		if got.Type != want.Type || got.Flags != want.Flags || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("case %d mismatch: %+v", i, got)
		}
	}
}

func TestDecodeFrameErrors(t *testing.T) {
	// 过短
	if _, err := DecodeFrame([]byte("LC")); err == nil {
		t.Error("short data should error")
	}

	// 坏魔数
	bad := []byte("XXXX")
	bad = append(bad, LCTPVersion, FramePing, 0, 0, 0, 0, 0, 0)
	if !errors.Is(DecodeFrameErr(bad), ErrBadMagic) {
		t.Errorf("bad magic should be ErrBadMagic, got %v", DecodeFrameErr(bad))
	}

	// 版本不支持
	badVer := append([]byte("LCTP"), 99, FramePing, 0, 0, 0, 0, 0, 0)
	if !errors.Is(DecodeFrameErr(badVer), ErrUnsupportedVer) {
		t.Errorf("bad version should be ErrUnsupportedVer, got %v", DecodeFrameErr(badVer))
	}

	// length 超过上限
	huge := append([]byte("LCTP"), LCTPVersion, FrameData, 0, 0)
	huge = append(huge, 0xFF, 0xFF, 0xFF, 0xFF)
	if !errors.Is(DecodeFrameErr(huge), ErrFrameTooLarge) {
		t.Errorf("huge length should be ErrFrameTooLarge, got %v", DecodeFrameErr(huge))
	}

	// length 与实际 payload 不一致
	inconsistent := append([]byte("LCTP"), LCTPVersion, FrameData, 0, 0, 0, 0, 0, 5, 1, 2, 3)
	if err := DecodeFrameErr(inconsistent); err == nil {
		t.Error("inconsistent length should error")
	}
}

func DecodeFrameErr(b []byte) error {
	_, err := DecodeFrame(b)
	return err
}

func TestFrameEncodeTooLarge(t *testing.T) {
	f := &Frame{Type: FrameData, Payload: make([]byte, MaxFramePayload+1)}
	if !errors.Is(encodeErr(f), ErrFrameTooLarge) {
		t.Errorf("Encode should reject oversized payload, got %v", encodeErr(f))
	}
}

func encodeErr(f *Frame) error {
	_, err := f.Encode()
	return err
}

func TestFramerOverTCP(t *testing.T) {
	a, b := net.Pipe()
	frA := NewFramer(a)
	frB := NewFramer(b)

	frames := []Frame{
		{Type: FrameHello, Payload: []byte("tok-1234")},
		{Type: FramePing},
		{Type: FrameData, Flags: 7, Payload: bytes.Repeat([]byte{0xAB}, 1_000_000)},
	}

	done := make(chan error, 1)
	go func() {
		for _, f := range frames {
			if err := frA.WriteFrame(&f); err != nil {
				done <- err
				return
			}
		}
		close(done)
	}()

	for i, want := range frames {
		got, err := frB.ReadFrame()
		if err != nil {
			t.Fatalf("case %d ReadFrame: %v", i, err)
		}
		if got.Type != want.Type || got.Flags != want.Flags || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("case %d mismatch: got type=%x flags=%x payloadLen=%d",
				i, got.Type, got.Flags, len(got.Payload))
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("writer: %v", err)
	}

	// 关闭后读取应得到 EOF / 错误
	a.Close()
	b.Close()
}

func TestFramerUnexpectedEOF(t *testing.T) {
	// 只写一个合法帧头、payload 写一半
	f := &Frame{Type: FrameData, Payload: []byte{1, 2, 3, 4}}
	data, _ := f.Encode()
	var buf bytes.Buffer
	buf.Write(data[:FrameHeaderSize+2]) // 截断 payload
	fr := NewFramer(&buf)
	if _, err := fr.ReadFrame(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated payload should be io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestFramerRejectsBadStream(t *testing.T) {
	fr := NewFramer(bytes.NewBufferString("not an lctp frame at all!!!"))
	if _, err := fr.ReadFrame(); !errors.Is(err, ErrBadMagic) {
		t.Errorf("expected ErrBadMagic, got %v", err)
	}
}

func TestFramerWriteRejectsOversized(t *testing.T) {
	fr := NewFramer(bytes.NewBuffer(nil))
	err := fr.WriteFrame(&Frame{Type: FrameData, Payload: make([]byte, MaxFramePayload+1)})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
}
