package mesh

import (
	"strings"
	"testing"
)

// TestTokenRoundtrip：生成的 Token 可读、哈希稳定、错 Token 哈希不同。
func TestTokenRoundtrip(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if len(tok) != 52 || strings.Contains(tok, "=") {
		t.Fatalf("token %q: want 52 chars, no padding", tok)
	}
	h1 := HashToken(tok)
	h2 := HashToken(tok)
	if h1 == "" || h1 != h2 {
		t.Fatal("HashToken must be deterministic non-empty")
	}
	if HashToken(tok) == HashToken(tok+"x") {
		t.Fatal("different tokens must hash differently")
	}
}

// TestPeerIDStable：同公钥指纹稳定、不同公钥不同指纹。
func TestPeerIDStable(t *testing.T) {
	id1, err := LoadOrCreateIdentity(t.TempDir() + "/id.bin")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadOrCreateIdentity(t.TempDir() + "/id.bin")
	if err != nil {
		t.Fatal(err)
	}
	p1 := PeerID(id1.PublicKey())
	p2 := PeerID(id1.PublicKey())
	if p1 != p2 {
		t.Fatal("PeerID must be deterministic")
	}
	if PeerID(id1.PublicKey()) == PeerID(id2.PublicKey()) {
		t.Fatal("different keys must map to different PeerIDs")
	}
	if len(PeerID(id1.PublicKey())) != 32 {
		t.Fatal("PeerID should be 32 hex chars")
	}
}

// TestAuthorizePeer：四种握手场景——
// 已知放行、未配 Token 时未知放行（旧 TOFU）、配 Token 时未知带对
// Token 加入、未知带错/无 Token 拒绝。
func TestAuthorizePeer(t *testing.T) {
	k := &KnownKeys{keys: map[string]string{}, names: map[string]string{}}
	id1, _ := LoadOrCreateIdentity(t.TempDir() + "/id1.bin")
	id2, _ := LoadOrCreateIdentity(t.TempDir() + "/id2.bin")
	joinHash := []byte(HashToken("secret-token"))

	// 1) 未知 + 未配 Token → 旧 TOFU：放行并记录。
	if _, err := k.authorizePeer(nil, id1.PublicKey(), "phone-a", ""); err != nil {
		t.Fatalf("no-token mode should admit unknown: %v", err)
	}
	if !k.IsKnown(PeerID(id1.PublicKey())) {
		t.Fatal("peer should be recorded")
	}
	// 2) 已记录 → 密钥握手直接放行，无需 Token。
	if _, err := k.authorizePeer(joinHash, id1.PublicKey(), "phone-a", ""); err != nil {
		t.Fatalf("known peer should pass without token: %v", err)
	}
	// 3) 未知 + 错 Token → 拒绝且不记录。
	if _, err := k.authorizePeer(joinHash, id2.PublicKey(), "phone-b", "wrong-token"); err == nil {
		t.Fatal("wrong token must be rejected")
	}
	if k.IsKnown(PeerID(id2.PublicKey())) {
		t.Fatal("rejected peer must not be recorded")
	}
	// 4) 未知 + 正确 Token → 授权加入并记录设备名。
	if _, err := k.authorizePeer(joinHash, id2.PublicKey(), "phone-b", "secret-token"); err != nil {
		t.Fatalf("correct token should admit: %v", err)
	}
	if !k.IsKnown(PeerID(id2.PublicKey())) {
		t.Fatal("authorized peer should be recorded")
	}
	if got := k.DeviceName(PeerID(id2.PublicKey())); got != "phone-b" {
		t.Fatalf("device name = %q, want phone-b", got)
	}
	// 5) 空白 Token（只有空格）也拒绝。
	id3, _ := LoadOrCreateIdentity(t.TempDir() + "/id3.bin")
	if _, err := k.authorizePeer(joinHash, id3.PublicKey(), "c", "  "); err == nil {
		t.Fatal("blank token must be rejected")
	}
}
