package mesh

// envelope_test.go 验证传输加密：Seal/Open 往返、错误密钥拒绝、
// TOFU 首次信任与密钥轮换拒绝。

import (
	"bytes"
	"path/filepath"
	"testing"
)

func testIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "id.bin"))
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	return id
}

// TestEnvelopeRoundTrip：A 加密 → B 解密（双方各自身份）。
func TestEnvelopeRoundTrip(t *testing.T) {
	a, b := testIdentity(t), testIdentity(t)

	msg := []byte(`{"c":{"node-a":5},"n":512}`)
	env, err := Seal(a, b.PublicKey(), msg)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !bytes.Equal(env.Key, a.PublicKey()) {
		t.Fatalf("envelope key = %x, want sender pub", env.Key)
	}

	plain, err := Open(b, env)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(plain, msg) {
		t.Fatalf("roundtrip mismatch: %s", plain)
	}

	// 第三方 C 无法解密（密钥不同）。
	c := testIdentity(t)
	if _, err := Open(c, env); err == nil {
		t.Fatal("third party decrypted, want error")
	}

	// 篡改密文 → 拒绝。
	tampered := *env
	tampered.Cipher = append([]byte{}, env.Cipher...)
	tampered.Cipher[0] ^= 0xff
	if _, err := Open(b, &tampered); err == nil {
		t.Fatal("tampered ciphertext accepted, want error")
	}

	// 篡改公钥 → 拒绝（AAD 绑定）。
	tamperedKey := *env
	tamperedKey.Key = append([]byte{}, env.Key...)
	tamperedKey.Key[0] ^= 0x01
	if _, err := Open(b, &tamperedKey); err == nil {
		t.Fatal("tampered key accepted, want error")
	}
}

// TestIdentityPersist：身份持久化——重载后公钥不变。
func TestIdentityPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id.bin")
	id1, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id1.PublicKey(), id2.PublicKey()) {
		t.Fatal("identity changed after reload")
	}
}

// TestKnownKeysTOFU：首次信任；同公钥放行；轮换拒绝。
func TestKnownKeysTOFU(t *testing.T) {
	k, err := LoadKnownKeys(filepath.Join(t.TempDir(), "known.json"))
	if err != nil {
		t.Fatal(err)
	}
	pub := testIdentity(t).PublicKey()

	if err := k.Trust("peer-1", pub); err != nil {
		t.Fatalf("first trust: %v", err)
	}
	if err := k.Trust("peer-1", pub); err != nil {
		t.Fatalf("same key re-trust: %v", err)
	}
	other := testIdentity(t).PublicKey()
	if err := k.Trust("peer-1", other); err == nil {
		t.Fatal("rotated key accepted, want TOFU error")
	}

	// 持久化后重载仍记住。
	k2, err := LoadKnownKeys(k.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := k2.PeerKey("peer-1"); !bytes.Equal(got, pub) {
		t.Fatal("known key not persisted")
	}
}
