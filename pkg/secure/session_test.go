package secure

// session_test.go 验证传输加密原语：双方独立派生同一密钥、
// Seal/Open 往返、篡改拒绝、挑战验证、TOFU 信任存储。

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"path/filepath"
	"testing"
)

// genKey 生成一个 X25519 密钥对。
func genKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestSessionMutual：双方用各自的（临时/持久）密钥独立派生同一会话密钥。
func TestSessionMutual(t *testing.T) {
	client := genKey(t)
	hub := genKey(t)

	cs, err := NewSession(client, hub.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	hs, err := NewSession(hub, client.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cs.Key(), hs.Key()) {
		t.Fatal("session keys differ")
	}
	if !bytes.Equal(cs.AAD(), hs.AAD()) {
		t.Fatal("AAD differs")
	}
}

// TestSessionRoundTrip：客户端 Seal → 服务端 Open。
func TestSessionRoundTrip(t *testing.T) {
	client, hub := genKey(t), genKey(t)
	cs, _ := NewSession(client, hub.PublicKey().Bytes())
	hs, _ := NewSession(hub, client.PublicKey().Bytes())

	msg := []byte(`{"k":2,"p":{"body":"hi"}}`)
	sealed, err := cs.Seal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// 密文不是明文（确实加密了）。
	if bytes.Contains(sealed, msg) {
		t.Fatal("sealed contains plaintext")
	}
	plain, err := hs.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, msg) {
		t.Fatalf("roundtrip mismatch: %s", plain)
	}

	// 篡改密文 → 拒绝。
	if len(sealed) < 20 {
		t.Fatal("sealed too short")
	}
	tampered := append([]byte(nil), sealed...)
	if sealed[10] == '0' {
		tampered[10] = '1'
	} else {
		tampered[10] = '0'
	}
	if _, err := hs.Open(tampered); err == nil {
		t.Fatal("tampered envelope accepted")
	}
}

// TestChallenge：hub 发挑战 → 客户端验证成功；错误密钥验证失败。
func TestChallenge(t *testing.T) {
	client, hub := genKey(t), genKey(t)
	cs, _ := NewSession(client, hub.PublicKey().Bytes())
	hs, _ := NewSession(hub, client.PublicKey().Bytes())

	nonce, ciphertext, err := hs.SealChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.VerifyChallenge(nonce, ciphertext); err != nil {
		t.Fatalf("verify challenge: %v", err)
	}

	// 篡改挑战 → 拒绝。
	tampered := append([]byte(nil), ciphertext...)
	tampered[0] ^= 0xff
	if err := cs.VerifyChallenge(nonce, tampered); err == nil {
		t.Fatal("tampered challenge accepted")
	}
}

// TestTrustStore：首次信任、同钥放行、轮换拒绝、持久化。
func TestTrustStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hubs.json")
	ts, err := LoadTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pub := genKey(t).PublicKey().Bytes()

	if err := ts.Trust("127.0.0.1:9000", pub); err != nil {
		t.Fatalf("first trust: %v", err)
	}
	if err := ts.Verify("127.0.0.1:9000", pub); err != nil {
		t.Fatalf("same key verify: %v", err)
	}
	if err := ts.Trust("127.0.0.1:9000", genKey(t).PublicKey().Bytes()); err == nil {
		t.Fatal("rotated key accepted, want TOFU error")
	}
	if err := ts.Trust("127.0.0.1:9001", pub); err != nil {
		t.Fatalf("second peer: %v", err)
	}

	// 重载后记住。
	ts2, err := LoadTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ts2.PubOf("127.0.0.1:9000"); !bytes.Equal(got, pub) {
		t.Fatal("trust not persisted")
	}
}
