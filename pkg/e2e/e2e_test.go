package e2e

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestIdentityPersist：身份持久化后重载同公钥（指纹稳定）。
func TestIdentityPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e2e_identity.bin")
	id1, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("identity perm = %o, want 0600", perm)
	}
	id2, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id1.PublicKey(), id2.PublicKey()) {
		t.Fatal("reload must yield same key")
	}
	if PeerID(id1.PublicKey()) != PeerID(id2.PublicKey()) {
		t.Fatal("PeerID must be stable")
	}
	if len(PeerID(id1.PublicKey())) != 32 {
		t.Fatal("PeerID should be 32 hex chars")
	}
}

// TestRoundtrip：单聊往返，两次加密信封不同（随机 DEK/eph）。
func TestRoundtrip(t *testing.T) {
	bob, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "b.bin"))
	msg := []byte("你好，局域网世界！hello e2e 123")

	e1, err := Encrypt(bob.PublicKey(), msg)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := Encrypt(bob.PublicKey(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Ciphertext == nil || bytes.Equal(e1.Ciphertext, e2.Ciphertext) {
		t.Fatal("same plaintext must produce different ciphertexts (random DEK)")
	}

	got, err := e1.Decrypt(bob.PrivateKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("roundtrip mismatch: %q != %q", got, msg)
	}

	// 序列化往返（信封要能放进消息 JSON）。
	s, err := e1.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	e3, err := Unmarshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got3, err := e3.Decrypt(bob.PrivateKey())
	if err != nil || !bytes.Equal(got3, msg) {
		t.Fatalf("marshal roundtrip failed: %v", err)
	}
}

// TestWrongKey / 篡改：错误接收者解不开，密文被篡改报错。
func TestWrongKeyAndTamper(t *testing.T) {
	bob, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "b.bin"))
	eve, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "e.bin"))

	e, err := Encrypt(bob.PublicKey(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// 第三方（Eve）解不开：没有她的封装 → ErrNotRecipient。
	if _, err := e.Decrypt(eve.PrivateKey()); err != ErrNotRecipient {
		t.Fatalf("eve decrypt err = %v, want ErrNotRecipient", err)
	}
	// 篡改密文：GCM 认证失败 → ErrDecrypt。
	e.Ciphertext[0] ^= 0xFF
	if _, err := e.Decrypt(bob.PrivateKey()); err != ErrDecrypt {
		t.Fatalf("tamper err = %v, want ErrDecrypt", err)
	}
}

// TestMultiRecipient：群聊多接收者，每个成员可解、非成员不可解。
func TestMultiRecipient(t *testing.T) {
	members := make([]*Identity, 4)
	pubs := make([][]byte, 4)
	for i := range members {
		members[i], _ = LoadOrCreateIdentity(filepath.Join(t.TempDir(), "m.bin"))
		pubs[i] = members[i].PublicKey()
	}
	outsider, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "x.bin"))

	e, err := EncryptMulti(pubs, []byte("群消息：全员可见"))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Recipients) != 4 {
		t.Fatalf("recipients = %d, want 4", len(e.Recipients))
	}
	for _, m := range members {
		got, err := e.Decrypt(m.PrivateKey())
		if err != nil {
			t.Fatalf("member decrypt: %v", err)
		}
		if string(got) != "群消息：全员可见" {
			t.Fatalf("member got %q", got)
		}
	}
	if _, err := e.Decrypt(outsider.PrivateKey()); err != ErrNotRecipient {
		t.Fatalf("outsider err = %v, want ErrNotRecipient", err)
	}
	// 重复接收者去重。
	e2, err := EncryptMulti([][]byte{pubs[0], pubs[0], pubs[1]}, []byte("dedup"))
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.Recipients) != 2 {
		t.Fatalf("dedup recipients = %d, want 2", len(e2.Recipients))
	}
}

// TestRandomness：不同消息的 eph 公钥不同（防重放/关联）。
func TestRandomness(t *testing.T) {
	bob, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "b.bin"))
	e1, _ := Encrypt(bob.PublicKey(), []byte("a"))
	e2, _ := Encrypt(bob.PublicKey(), []byte("b"))
	if bytes.Equal(e1.Ephemeral, e2.Ephemeral) {
		t.Fatal("ephemeral keys must differ per message")
	}
	// 随机性来源健全性：即使明文相同也各不相同（已在 TestRoundtrip 覆盖）。
	_ = rand.Reader
}
