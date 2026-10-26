package transfer

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestKeyEnvelopeRoundTrip 验证两端 X25519 ECDH 派生 KEK 后，文件密钥可封装/解封。
func TestKeyEnvelopeRoundTrip(t *testing.T) {
	senderPriv, err := generateX25519()
	if err != nil {
		t.Fatalf("sender key: %v", err)
	}
	receiverPriv, err := generateX25519()
	if err != nil {
		t.Fatalf("receiver key: %v", err)
	}

	// 发送方从接收公钥派生 KEK，接收方从发送公钥派生 KEK，二者必须一致。
	kekBySender, err := deriveKEK(senderPriv, receiverPriv.PublicKey())
	if err != nil {
		t.Fatalf("sender kek: %v", err)
	}
	kekByReceiver, err := deriveKEK(receiverPriv, senderPriv.PublicKey())
	if err != nil {
		t.Fatalf("receiver kek: %v", err)
	}
	if !bytes.Equal(kekBySender, kekByReceiver) {
		t.Fatal("ECDH 派生的 KEK 两端不一致")
	}

	fileKey, err := generateFileKey()
	if err != nil {
		t.Fatalf("file key: %v", err)
	}

	wrapped, err := wrapKey(kekByReceiver, fileKey)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := unwrapKey(kekBySender, wrapped)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, fileKey) {
		t.Fatal("解封出的文件密钥与原密钥不一致")
	}
}

// TestPubKeyEncodeRoundTrip 验证公钥 base64 编解码一致。
func TestPubKeyEncodeRoundTrip(t *testing.T) {
	priv, err := generateX25519()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s := encodePub(priv.PublicKey())
	pub, err := decodePub(s)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(pub.Bytes(), priv.PublicKey().Bytes()) {
		t.Fatal("公钥编解码不一致")
	}
}

// TestUnwrapWrongKey 验证用错误 KEK 解封应失败。
func TestUnwrapWrongKey(t *testing.T) {
	kek1, _ := generateFileKey()
	kek2, _ := generateFileKey()
	fileKey, _ := generateFileKey()

	wrapped, err := wrapKey(kek1, fileKey)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := unwrapKey(kek2, wrapped); err == nil {
		t.Fatal("用错误 KEK 解封应当失败")
	}
}

// TestUnwrapTampered 验证篡改封装密文后解封失败。
func TestUnwrapTampered(t *testing.T) {
	kek, _ := generateFileKey()
	fileKey, _ := generateFileKey()
	wrapped, err := wrapKey(kek, fileKey)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	// 翻转最后一个字符对应的字符（base64 文本），制造篡改。
	tampered := wrapped[:len(wrapped)-2]
	if wrapped[len(wrapped)-2] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	tampered += wrapped[len(wrapped)-1:]
	if _, err := unwrapKey(kek, tampered); err == nil {
		t.Fatal("篡改后的封装应当解封失败")
	}
}

// TestNewGCMRejectsBadKey 验证非法密钥长度被拒。
func TestNewGCMRejectsBadKey(t *testing.T) {
	if _, err := newGCM(make([]byte, 7)); err == nil {
		t.Fatal("非法长度密钥应当被拒")
	}
}

// TestECDHTextRoundTrip 验证 M15 零配置 X25519 ECDH 文本加解密往返与篡改拒绝。
func TestECDHTextRoundTrip(t *testing.T) {
	alicePriv, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	bobPriv, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	alicePub := EncodePub(alicePriv.PublicKey())
	bobPub := EncodePub(bobPriv.PublicKey())

	wire, err := SealECDHText(alicePriv, bobPub, "零配置端到端加密消息")
	if err != nil {
		t.Fatalf("SealECDHText: %v", err)
	}
	if len(wire) <= len(TextECDHPrefix) || wire[:len(TextECDHPrefix)] != TextECDHPrefix {
		t.Fatalf("expected prefix %s, got %q", TextECDHPrefix, wire)
	}

	got, err := OpenECDHText(bobPriv, alicePub, wire)
	if err != nil {
		t.Fatalf("OpenECDHText: %v", err)
	}
	if got != "零配置端到端加密消息" {
		t.Fatalf("got %q", got)
	}

	// 错误接收者私钥应当解密失败。
	malloryPriv, _ := GenerateX25519()
	if _, err := OpenECDHText(malloryPriv, alicePub, wire); err == nil {
		t.Fatal("expected mallory decrypt failure")
	}
}

// TestChannelKeyAndBandwidthLimiter 验证 M19 频道群组密钥派生/加解密与 M21 带宽限速器。
func TestChannelKeyAndBandwidthLimiter(t *testing.T) {
	ownerPriv, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	chKey, err := DeriveChannelKey(ownerPriv, "chan-01")
	if err != nil || chKey == "" {
		t.Fatalf("DeriveChannelKey: %v", err)
	}
	if _, err := DeriveChannelKey(nil, "chan-01"); err == nil {
		t.Fatal("expected error for nil ownerPriv")
	}

	wire, err := SealChannelText(chKey, "频道端到端加密消息")
	if err != nil {
		t.Fatalf("SealChannelText: %v", err)
	}
	plain, err := OpenChannelText(chKey, wire)
	if err != nil || plain != "频道端到端加密消息" {
		t.Fatalf("OpenChannelText got %q, err %v", plain, err)
	}
	if _, err := OpenChannelText("invalid-key", wire); err == nil {
		t.Fatal("expected error on invalid channel key")
	}

	SetBandwidthLimitBps(50 * 1024 * 1024)
	if BandwidthLimitBps() != 50*1024*1024 {
		t.Fatalf("unexpected limit %d", BandwidthLimitBps())
	}
	if err := throttleBytes(context.Background(), 4096); err != nil {
		t.Fatalf("throttleBytes: %v", err)
	}
	SetBandwidthLimitBps(0)
	if BandwidthLimitBps() != 0 {
		t.Fatalf("expected 0 limit, got %d", BandwidthLimitBps())
	}

	histKey, err := DeriveHistoryKey(ownerPriv)
	if err != nil || histKey == "" {
		t.Fatalf("DeriveHistoryKey: %v", err)
	}
	sealedLine, err := SealHistoryLine(histKey, []byte(`{"id":"m1","text":"secret"}`))
	if err != nil || !strings.HasPrefix(sealedLine, TextHistoryPrefix) {
		t.Fatalf("SealHistoryLine got %q, err %v", sealedLine, err)
	}
	openedLine, err := OpenHistoryLine(histKey, sealedLine)
	if err != nil || string(openedLine) != `{"id":"m1","text":"secret"}` {
		t.Fatalf("OpenHistoryLine got %q, err %v", string(openedLine), err)
	}
}
