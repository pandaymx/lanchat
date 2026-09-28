package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/relay"
)

// makeTestFile 在 dir 下生成 size 字节可预测内容文件，返回路径与 hex SHA-256。
func makeTestFile(t *testing.T, dir string, size int64) (path, sum string) {
	t.Helper()
	path = filepath.Join(dir, "source.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	buf := make([]byte, 1<<16)
	var remaining int64 = size
	for remaining > 0 {
		for i := range buf {
			buf[i] = byte((remaining + int64(i)) % 251)
		}
		n := int64(len(buf))
		if n > remaining {
			n = remaining
		}
		if _, err := f.Write(buf[:n]); err != nil {
			t.Fatalf("write source: %v", err)
		}
		remaining -= n
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	h := sha256.New()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source for hash: %v", err)
	}
	h.Write(raw)
	return path, hex.EncodeToString(h.Sum(nil))
}

// TestP2PForwardLoopback 正向 P2P：Sender 监听，Receiver 拨号，全量落盘并校验。
func TestP2PForwardLoopback(t *testing.T) {
	dir := t.TempDir()
	const size = 3 << 20 // 3 MiB，跨多个 chunk 与信用窗口
	srcPath, sum := makeTestFile(t, dir, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()

	sender, err := NewSender(SendConfig{
		TransferID: "tf-fwd",
		File:       src,
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "tok-fwd",
	})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	defer sender.Close()

	destPath := filepath.Join(dir, "recv", "source.bin")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 用 127.0.0.1 + sender 实际端口组装可达候选，避免依赖本机 LAN 网卡。
	candidate := "127.0.0.1" + sender.Addr().String()[strings.LastIndex(sender.Addr().String(), ":"):]

	srvErr := make(chan error, 1)
	go func() { srvErr <- sender.Serve(ctx, nil) }()

	err = Receive(ctx, ReceiveConfig{
		TransferID: "tf-fwd",
		Token:      "tok-fwd",
		DestPath:   destPath,
		Candidates: []string{candidate},
		Size:       size,
		SHA256:     sum,
	}, nil)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-srvErr; err != nil {
		t.Fatalf("sender serve: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if int64(len(got)) != size || hex.EncodeToString(bytesHash(got)) != sum {
		t.Fatalf("dest content mismatch: len=%d", len(got))
	}
}

// TestP2PReverseLoopback 反向拨号：Receiver 监听，Sender 用 DialSend 拨入。
func TestP2PReverseLoopback(t *testing.T) {
	dir := t.TempDir()
	const size = 2 << 20
	srcPath, sum := makeTestFile(t, dir, size)
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()

	receiver, err := NewReceiver(ReceiveConfig{
		TransferID: "tf-rev",
		Token:      "tok-rev",
		DestPath:   filepath.Join(dir, "recv", "source.bin"),
		Size:       size,
		SHA256:     sum,
	})
	if err != nil {
		t.Fatalf("new receiver: %v", err)
	}
	defer receiver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	candidate := "127.0.0.1" + receiver.Addr().String()[strings.LastIndex(receiver.Addr().String(), ":"):]

	sndErr := make(chan error, 1)
	go func() {
		sndErr <- DialSend(ctx, SendConfig{
			TransferID: "tf-rev",
			File:       src,
			Name:       "source.bin",
			Size:       size,
			SHA256:     sum,
			Token:      "tok-rev",
		}, []string{candidate}, 5*time.Second, nil)
	}()

	if err := receiver.Serve(ctx, nil); err != nil {
		t.Fatalf("receiver serve: %v", err)
	}
	if err := <-sndErr; err != nil {
		t.Fatalf("dial send: %v", err)
	}
}

// TestRelayLoopback 经真实中继的加密数据路径，端到端落盘校验。
func TestRelayLoopback(t *testing.T) {
	dir := t.TempDir()
	const size = 1 << 20
	srcPath, sum := makeTestFile(t, dir, size)

	srv, err := relay.New("127.0.0.1:0")
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	relayCtx, stopRelay := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(relayCtx) }()
	t.Cleanup(func() { stopRelay(); _ = srv.Close() })
	relayAddr := srv.AdvertiseAddr("127.0.0.1")
	if relayAddr == "" {
		t.Fatal("empty relay advertise addr")
	}

	fileKey, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("file key: %v", err)
	}
	rc := RelayConfig{
		RelayAddr:  relayAddr,
		RelayID:    "relay-loop-id",
		FileKey:    fileKey,
		TransferID: "tf-relay-loop",
		Token:      "tok-relay",
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
	}

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()
	destPath := filepath.Join(dir, "recv", "source.bin")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sndErr := make(chan error, 1)
	go func() { sndErr <- RelaySend(ctx, rc, src, nil) }()
	if err := RelayReceive(ctx, rc, destPath, nil); err != nil {
		t.Fatalf("relay receive: %v", err)
	}
	if err := <-sndErr; err != nil {
		t.Fatalf("relay send: %v", err)
	}
}

// TestCryptoRoundtrip 覆盖导出密码学包装：X25519、KEK、信封加解密。
func TestCryptoRoundtrip(t *testing.T) {
	senderPriv, err := GenerateX25519()
	if err != nil {
		t.Fatalf("sender key: %v", err)
	}
	receiverPriv, err := GenerateX25519()
	if err != nil {
		t.Fatalf("receiver key: %v", err)
	}

	// base64 公钥往返。
	enc := EncodePub(senderPriv.PublicKey())
	pub, err := DecodePub(enc)
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	if EncodePub(pub) != enc {
		t.Fatal("pub roundtrip mismatch")
	}

	// 双方独立 ECDH 应得同一 KEK。
	kekR, err := DeriveKEK(receiverPriv, senderPriv.PublicKey())
	if err != nil {
		t.Fatalf("receiver kek: %v", err)
	}
	kekS, err := DeriveKEK(senderPriv, receiverPriv.PublicKey())
	if err != nil {
		t.Fatalf("sender kek: %v", err)
	}
	if !bytes.Equal(kekR, kekS) {
		t.Fatal("KEK mismatch")
	}

	fileKey, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("file key: %v", err)
	}
	wrapped, err := WrapKey(kekR, fileKey)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := UnwrapKey(kekS, wrapped)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, fileKey) {
		t.Fatal("file key roundtrip mismatch")
	}
}

// TestCryptoErrors 覆盖密码学错误分支。
func TestCryptoErrors(t *testing.T) {
	if _, err := DecodePub("!!!not-base64!!!"); err == nil {
		t.Fatal("want decode pub error")
	}
	if _, err := DecodePub(hex.EncodeToString([]byte("too-short"))); err == nil {
		t.Fatal("want invalid point error")
	}
	if _, err := wrapKey([]byte{1, 2}, nil); err == nil {
		t.Fatal("want bad key error")
	}
	if _, err := unwrapKey(make([]byte, 32), "===="); err == nil {
		t.Fatal("want decode wrapped error")
	}
	if _, err := unwrapKey(make([]byte, 32), hex.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("want too-short envelope error")
	}
}

// TestEncConnRoundtrip 在 net.Pipe 上验证加密流的小帧刷新与透传方法。
func TestEncConnRoundtrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	key, _ := GenerateFileKey()
	a, err := newEncConn(client, key)
	if err != nil {
		t.Fatalf("enc a: %v", err)
	}
	b, err := newEncConn(server, key)
	if err != nil {
		t.Fatalf("enc b: %v", err)
	}

	msg := []byte("hello-lctp-handshake-frame")
	done := make(chan error, 1)
	go func() {
		_, err := a.Write(msg)
		done <- err
	}()

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(b, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatal("plaintext mismatch")
	}
	if err := <-done; err != nil {
		t.Fatalf("write: %v", err)
	}

	// 透传方法不应 panic / 报错。
	_ = a.SetDeadline(time.Now().Add(time.Second))
	_ = a.SetReadDeadline(time.Now().Add(time.Second))
	_ = a.SetWriteDeadline(time.Now().Add(time.Second))
	if a.LocalAddr() == nil || a.RemoteAddr() == nil {
		t.Fatal("addr passthrough returned nil")
	}
}

// TestNewSenderValidation 覆盖构造校验与候选/地址方法。
func TestNewSenderValidation(t *testing.T) {
	if _, err := NewSender(SendConfig{}); err == nil {
		t.Fatal("want validation error")
	}
	dir := t.TempDir()
	srcPath, _ := makeTestFile(t, dir, 16)
	src, _ := os.Open(srcPath)
	defer src.Close()

	s, err := NewSender(SendConfig{File: src, Token: "t"})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	defer s.Close()
	if s.Addr() == nil {
		t.Fatal("nil addr")
	}
	_ = s.CandidateAddrs()
	s.Close()
	s.Close() // closeOnce 幂等

	r, err := NewReceiver(ReceiveConfig{})
	if err != nil {
		t.Fatalf("new receiver: %v", err)
	}
	if r.Addr() == nil {
		t.Fatal("nil addr")
	}
	_ = r.CandidateAddrs()
	r.Close()
	r.Close()
}

// TestReceiveValidation 覆盖 Receive 的入参校验与不可达拨号。
func TestReceiveValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Receive(ctx, ReceiveConfig{}, nil); err != ErrDialFailed {
		t.Fatalf("want no-candidate error, got %v", err)
	}
}

// TestSubnetPref 覆盖候选同子网优先排序。
func TestSubnetPref(t *testing.T) {
	in := []string{
		"203.0.113.5:1000",
		"192.168.99.7:1000",
	}
	out := preferLocalSubnet(append([]string(nil), in...))
	if len(out) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(out))
	}
	// sameSubnet / sharesSubnetWith 分支。
	if sameSubnet(net.ParseIP("10.0.0.1").To4(), net.ParseIP("10.0.0.9").To4()) != true {
		t.Fatal("want same subnet")
	}
	if sameSubnet(nil, nil) {
		t.Fatal("nil should not share subnet")
	}
	if isCGNATIP(net.ParseIP("100.64.0.1").To4()) != true {
		t.Fatal("want CGNAT")
	}
	if isVirtualIfaceName("wlan0") {
		t.Fatal("wlan0 is not virtual")
	}
	if !isVirtualIfaceName("docker0") {
		t.Fatal("docker0 is virtual")
	}
	_ = parseCandidateIP("127.0.0.1:9")
	_ = parseCandidateIP("bad")
	_ = candidateAddrs("0")
	_ = localPrivateIPv4()
}

// TestReadJSONErrorBranches 覆盖 readJSON 的类型不符与 ERROR 帧分支。
func TestReadJSONErrorBranches(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go func() {
		fr := protocol.NewFramer(b)
		_ = writePeerError(fr, "boom")
	}()
	fr := protocol.NewFramer(a)
	var hb helloBody
	if err := readJSON(fr, protocol.FrameHello, &hb); err == nil {
		t.Fatal("want peer error")
	}
}

// bytesHash 返回内容的 SHA-256。
func bytesHash(b []byte) []byte {
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}
