package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/relay"
	"github.com/pandaymx/lanchat/internal/server"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// tapProxy 是位于客户端与真实中继之间的窃听代理：转发全部字节的同时，
// 记录链路上经过的每个字节，供断言「中继只见密文、从未出现明文」。
type tapProxy struct {
	listener net.Listener
	target   string

	mu      sync.Mutex
	capture []byte
}

func newTapProxy(t *testing.T, target string) *tapProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tap listen: %v", err)
	}
	tp := &tapProxy{listener: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go tp.serve()
	return tp
}

func (tp *tapProxy) addr() string { return tp.listener.Addr().String() }

func (tp *tapProxy) bytes() []byte {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	out := make([]byte, len(tp.capture))
	copy(out, tp.capture)
	return out
}

func (tp *tapProxy) record(b []byte) {
	tp.mu.Lock()
	tp.capture = append(tp.capture, b...)
	tp.mu.Unlock()
}

func (tp *tapProxy) serve() {
	for {
		conn, err := tp.listener.Accept()
		if err != nil {
			return
		}
		go tp.handle(conn)
	}
}

func (tp *tapProxy) handle(in net.Conn) {
	defer in.Close()
	out, err := net.Dial("tcp", tp.target)
	if err != nil {
		return
	}
	defer out.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(out, io.TeeReader(in, &recordWriter{tp: tp}))
		_ = closeWrite(out)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(in, io.TeeReader(out, &recordWriter{tp: tp}))
		_ = closeWrite(in)
		done <- struct{}{}
	}()
	// 等两个方向都结束，避免单向半关闭后提前拆除另一方向的尾数据 / ACK。
	<-done
	<-done
}

// recordWriter 把写入的字节同步记录到 tapProxy。
type recordWriter struct{ tp *tapProxy }

func (w *recordWriter) Write(p []byte) (int, error) {
	w.tp.record(p)
	return len(p), nil
}

func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// runRelayTransfer 经给定中继地址完成一次中继发送/接收，返回落盘路径。
func runRelayTransfer(t *testing.T, srcPath string, relayAddr, relayID string, fileKey []byte, size int64, sum string) string {
	t.Helper()
	const transferID = "tf-relay"
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("打开源文件: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	destPath := filepath.Join(t.TempDir(), "recv", "source.bin")

	rc := transfer.RelayConfig{
		RelayAddr:  relayAddr,
		RelayID:    relayID,
		FileKey:    fileKey,
		TransferID: transferID,
		Token:      "relay-token",
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
	}

	ctx, cancel := contextWithTimeout(15 * time.Second)
	defer cancel()

	sendErr := make(chan error, 1)
	go func() { sendErr <- transfer.RelaySend(ctx, rc, src, nil) }()
	recvErr := make(chan error, 1)
	go func() { recvErr <- transfer.RelayReceive(ctx, rc, destPath, nil) }()

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("RelaySend: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("发送方未在 15s 内完成")
	}
	select {
	case err := <-recvErr:
		if err != nil {
			t.Fatalf("RelayReceive: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("接收方未在 5s 内结束")
	}
	return destPath
}

// startStandaloneRelay 启动一个独立中继并注册清理，返回其服务器。
func startStandaloneRelay(t *testing.T) *relay.Server {
	t.Helper()
	relaySrv, err := relay.New("127.0.0.1:0")
	if err != nil {
		t.Fatalf("relay new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = relaySrv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = relaySrv.Close()
	})
	return relaySrv
}

// TestRelayForceCiphertext 模拟 relay.force=true：两端只与中继通信。
// 验证落盘 SHA-256 正确，且中继链路上抓到的字节（剔除明文握手行后）
// 不含任何明文片段。
func TestRelayForceCiphertext(t *testing.T) {
	const size = 3 << 20 // 3 MiB，跨多个加密 block 与数据块
	srcPath, sum := makeSourceFile(t, size)
	plain, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("读取明文: %v", err)
	}

	// 真实中继 + 窃听代理（客户端只连代理，等价于只连中继）。
	relaySrv := startStandaloneRelay(t)
	tp := newTapProxy(t, relaySrv.Addr().String())

	fileKey, err := transfer.GenerateFileKey()
	if err != nil {
		t.Fatalf("gen file key: %v", err)
	}
	const relayID = "relay-force-id"

	destPath := runRelayTransfer(t, srcPath, tp.addr(), relayID, fileKey, size, sum)

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("读取落盘: %v", err)
	}
	if int64(len(got)) != size {
		t.Fatalf("落盘大小 = %d want %d", len(got), size)
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != sum {
		t.Fatal("中继落盘 SHA-256 不一致")
	}

	// 剔除两条连接各自的明文握手行（relayID）后，其余应全为密文。
	captured := bytes.ReplaceAll(tp.bytes(), []byte(relayID), nil)
	// 明文断言：取多处 256B 窗口，任何窗口都不应在线路出现。
	for off := 0; off+256 <= len(plain); off += 512 {
		if bytes.Contains(captured, plain[off:off+256]) {
			t.Fatalf("中继链路在偏移 %d 处出现 256B 明文片段", off)
		}
	}
}

// TestRelaySignalingOrchestration 验证信令侧编排：
// RELAY_REQUEST 后双方各收 RELAY_GRANT（同 relayID、含 relayAddr）；
// 接收方经端到端 X25519 信封回 RELAY_KEY，发送方解密还原文件密钥，
// 且服务器转发时 From 被盖为接收方；非法请求与未知对端分别得到
// ERROR(invalid_relay_request) 与 ACK(undeliverable)。
func TestRelaySignalingOrchestration(t *testing.T) {
	url := startTestServerOpts(t, func(o *server.Options) {
		o.RelayListen = "127.0.0.1:0"
		o.RelayHost = "127.0.0.1"
	})
	sender := dialClient(t, url, testPSK, "Alice")
	receiver := dialClient(t, url, testPSK, "Bob")

	const transferID = "tf-sig"

	// 1. 发送方在 FILE_OFFER 带一次性 X25519 公钥。
	senderPriv, err := transfer.GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	sender.sendTo(protocol.FileOffer, receiver.id, protocol.FileOfferPayload{
		TransferID: transferID,
		Name:       "source.bin",
		Size:       1024,
		ECDHPub:    transfer.EncodePub(senderPriv.PublicKey()),
	})
	if offer := receiver.recvUntil(protocol.FileOffer, readTimeout); offer.From != sender.id {
		t.Fatalf("offer From = %q", offer.From)
	}

	// 2. 接收方直连失败，发 RELAY_REQUEST。
	receiver.sendTo(protocol.RelayRequest, "", protocol.RelayRequestPayload{
		TransferID: transferID,
		PeerID:     sender.id,
	})

	// 3. 双方各收 RELAY_GRANT。
	g1 := receiver.recvUntil(protocol.RelayGrant, readTimeout)
	g2 := sender.recvUntil(protocol.RelayGrant, readTimeout)
	var gp1, gp2 protocol.RelayGrantPayload
	if err := g1.DecodePayload(&gp1); err != nil {
		t.Fatal(err)
	}
	if err := g2.DecodePayload(&gp2); err != nil {
		t.Fatal(err)
	}
	if gp1.RelayID == "" || gp1.RelayID != gp2.RelayID {
		t.Fatalf("双方 relayID 不一致或为空: %q / %q", gp1.RelayID, gp2.RelayID)
	}
	if gp1.RelayAddr == "" {
		t.Fatal("RELAY_GRANT 缺少 relayAddr")
	}

	// 4. 接收方生成临时 X25519 + 文件密钥，封装后回 RELAY_KEY。
	receiverPriv, err := transfer.GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	fileKey, err := transfer.GenerateFileKey()
	if err != nil {
		t.Fatal(err)
	}
	kekByReceiver, err := transfer.DeriveKEK(receiverPriv, senderPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := transfer.WrapKey(kekByReceiver, fileKey)
	if err != nil {
		t.Fatal(err)
	}
	receiver.sendTo(protocol.RelayKey, sender.id, protocol.RelayKeyPayload{
		RelayID:    gp1.RelayID,
		TransferID: transferID,
		PeerPub:    transfer.EncodePub(receiverPriv.PublicKey()),
		WrappedKey: wrapped,
	})

	keyEnv := sender.recvUntil(protocol.RelayKey, readTimeout)
	if keyEnv.From != receiver.id {
		t.Fatalf("RELAY_KEY From = %q，期望接收方 %q", keyEnv.From, receiver.id)
	}
	var kp protocol.RelayKeyPayload
	if err := keyEnv.DecodePayload(&kp); err != nil {
		t.Fatal(err)
	}
	peerPub, err := transfer.DecodePub(kp.PeerPub)
	if err != nil {
		t.Fatal(err)
	}
	kekBySender, err := transfer.DeriveKEK(senderPriv, peerPub)
	if err != nil {
		t.Fatal(err)
	}
	gotKey, err := transfer.UnwrapKey(kekBySender, kp.WrappedKey)
	if err != nil {
		t.Fatalf("发送方解密文件密钥失败: %v", err)
	}
	if !bytes.Equal(gotKey, fileKey) {
		t.Fatal("端到端协商出的文件密钥不一致")
	}

	// 5. 非法请求 → ERROR(invalid_relay_request)。
	receiver.sendTo(protocol.RelayRequest, "", protocol.RelayRequestPayload{TransferID: ""})
	errEnv := receiver.recvUntil(protocol.Error, readTimeout)
	var epp protocol.ErrorPayload
	if err := errEnv.DecodePayload(&epp); err != nil {
		t.Fatal(err)
	}
	if epp.Code != "invalid_relay_request" {
		t.Fatalf("错误码 = %q，期望 invalid_relay_request", epp.Code)
	}

	// 6. 对端不存在 → ACK(undeliverable)。
	req := receiver.sendTo(protocol.RelayRequest, "", protocol.RelayRequestPayload{
		TransferID: transferID,
		PeerID:     "ghost-id",
	})
	ack := receiver.recvUntil(protocol.MsgAck, readTimeout)
	var ap protocol.MsgAckPayload
	if err := ack.DecodePayload(&ap); err != nil {
		t.Fatal(err)
	}
	if ap.Status != "undeliverable" || ack.ReplyTo != req.ID {
		t.Fatalf("ACK = %q / replyTo %q", ap.Status, ack.ReplyTo)
	}
}

// TestRelayAutoFallback 验证直连失败后自动回退中继：先以不可达候选触发
// 直连失败，编排层随即经中继完成传输，落盘 SHA-256 正确。
// 这里显式编排两段路径以模拟真实客户端「直连优先、中继兜底」的决策。
func TestRelayAutoFallback(t *testing.T) {
	const size = 1 << 20 // 1 MiB
	srcPath, sum := makeSourceFile(t, size)

	relaySrv := startStandaloneRelay(t)
	fileKey, err := transfer.GenerateFileKey()
	if err != nil {
		t.Fatal(err)
	}

	// 阶段一：直连一个保证不可达的地址，必须失败。
	dialCtx, dialCancel := contextWithTimeout(time.Second)
	defer dialCancel()
	directErr := transfer.DialSend(dialCtx, transfer.SendConfig{
		TransferID: "tf-fallback",
		Name:       "source.bin",
		Size:       size,
		SHA256:     sum,
		Token:      "relay-token",
	}, []string{"127.0.0.1:1"}, time.Second, nil)
	if directErr == nil {
		t.Fatal("直连不可达地址应当失败")
	}

	// 阶段二：自动回退中继并成功。
	relayAddr := relaySrv.AdvertiseAddr("127.0.0.1")
	destPath := runRelayTransfer(t, srcPath, relayAddr, "relay-fallback-id", fileKey, size, sum)

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("读取落盘: %v", err)
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != sum {
		t.Fatal("回退中继落盘 SHA-256 不一致")
	}
}
