package transfer

import (
	"bytes"
	"testing"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// TestHelloRoundTrip 验证 HELLO JSON 帧编解码往返。
func TestHelloRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writeHello(fr, "secret-token", 0); err != nil {
		t.Fatal(err)
	}
	got, err := readHello(fr)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "secret-token" || got.ResumeOffset != 0 {
		t.Fatalf("往返 = %+v", got)
	}
}

// TestAcceptRoundTrip 验证 ACCEPT 帧往返。
func TestAcceptRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writeAccept(fr, 123456, ChunkSize); err != nil {
		t.Fatal(err)
	}
	got, err := readAccept(fr)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 123456 || got.ChunkSize != ChunkSize {
		t.Fatalf("往返 = %+v", got)
	}
}

// TestFinRoundTrip 验证 FIN 帧往返。
func TestFinRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writeFin(fr, "abc123", 999); err != nil {
		t.Fatal(err)
	}
	got, err := readFin(fr)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != "abc123" || got.TotalBytes != 999 {
		t.Fatalf("往返 = %+v", got)
	}
}

// TestDataRoundTrip 验证 DATA 帧 offset 与数据往返。
func TestDataRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	payload := bytes.Repeat([]byte{0xAB}, 1000)
	if err := writeData(fr, 4096, payload); err != nil {
		t.Fatal(err)
	}
	off, data, err := readData(fr)
	if err != nil {
		t.Fatal(err)
	}
	if off != 4096 || !bytes.Equal(data, payload) {
		t.Fatalf("offset=%d dataLen=%d，期望 4096/1000", off, len(data))
	}
}

// TestAckRoundTrip 验证 ACK 帧往返。
func TestAckRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writeAck(fr, 8192, MaxCreditBytes); err != nil {
		t.Fatal(err)
	}
	cum, credit, err := readAck(fr)
	if err != nil {
		t.Fatal(err)
	}
	if cum != 8192 || credit != MaxCreditBytes {
		t.Fatalf("cum=%d credit=%d，期望 8192/%d", cum, credit, MaxCreditBytes)
	}
}

// TestReadDataCancel 验证收到 CANCEL 时 readData 返回 ErrCanceled。
func TestReadDataCancel(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writeCancel(fr); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readData(fr); err != ErrCanceled {
		t.Fatalf("err = %v，期望 ErrCanceled", err)
	}
}

// TestReadPeerError 验证 ERROR 帧被包装为 ErrPeerError。
func TestReadPeerError(t *testing.T) {
	var buf bytes.Buffer
	fr := protocol.NewFramer(&buf)
	if err := writePeerError(fr, "boom"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readData(fr); err == nil || err.Error() == "" {
		t.Fatal("期望 peer error")
	}
}
