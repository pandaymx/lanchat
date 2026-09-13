package mesh

// envelope.go 是 mesh 同步通道的传输加密封装（ADR-014 wire v2）。
//
// 每个同步请求/响应都是一个 Envelope：
//   - Key：发送方公钥（raw 32B，每请求携带，接收方据此算共享密钥）
//   - Nonce：12B 随机数（GCM 一次性 nonce）
//   - Cipher：AES-256-GCM 密文（含 16B tag），明文是 SyncRequest /
//     []SyncResponse 的 JSON
//
// 密钥派生：双方静态 ECDH → HKDF-SHA256 → 32B 会话密钥。无状态：
// 每请求都带公钥，双方各自可算，不需要握手轮次。
//
// TOFU（Trust On First Use）：接收方首次见到对端公钥时记录；
// 之后同身份（客户端=URL、服务端=来源 IP）的公钥发生变化 → 拒绝，
// 防密钥轮换与中间人替换。局域网免鉴权信任模型不变（见 ADR-014），
// 本层只保证保密性与完整性（防窃听/防篡改）。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Envelope 是线缆上的加密封装。
type Envelope struct {
	Key    []byte `json:"k,omitempty"` // 发送方 X25519 公钥
	Nonce  []byte `json:"n"`           // 12B GCM nonce
	Cipher []byte `json:"c"`           // AEAD 密文（含 tag）
}

// Seal 加密 plaintext 给 peerPub 持有者，返回 Envelope（Key=本节点公钥）。
func Seal(id *Identity, peerPub, plaintext []byte) (*Envelope, error) {
	key, err := sessionKey(id, peerPub)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("mesh: nonce: %w", err)
	}
	aad := aadFor(id.PublicKey(), peerPub)
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		Key:    id.PublicKey(),
		Nonce:  nonce,
		Cipher: aead.Seal(nil, nonce, plaintext, aad),
	}, nil
}

// Open 解密 Envelope（env.Key 是发送方公钥，即本方 peerPub）。
func Open(id *Identity, env *Envelope) ([]byte, error) {
	if len(env.Nonce) != 12 {
		return nil, fmt.Errorf("mesh: bad nonce length %d", len(env.Nonce))
	}
	key, err := sessionKey(id, env.Key)
	if err != nil {
		return nil, err
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	aad := aadFor(id.PublicKey(), env.Key)
	plain, err := aead.Open(nil, env.Nonce, env.Cipher, aad)
	if err != nil {
		return nil, fmt.Errorf("mesh: decrypt: %w", err)
	}
	return plain, nil
}

// sessionKey 计算与 peerPub 的会话密钥（ECDH → HKDF）。
func sessionKey(id *Identity, peerPub []byte) ([]byte, error) {
	shared, err := id.SharedKey(peerPub)
	if err != nil {
		return nil, err
	}
	return deriveKey(shared, id.PublicKey(), peerPub), nil
}

// aadFor 返回 GCM 附加认证数据：双方公钥排序拼接，绑定密钥对。
func aadFor(pubA, pubB []byte) []byte {
	if string(pubA) <= string(pubB) {
		return append(append([]byte{}, pubA...), pubB...)
	}
	return append(append([]byte{}, pubB...), pubA...)
}

// gcm 构造 AES-256-GCM AEAD。
func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mesh: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("mesh: gcm: %w", err)
	}
	return aead, nil
}

// ---- TOFU 公钥信任表 ----

// KnownKeys 记录已信任的对端公钥（peerID → base64 pubkey），
// JSON 文件持久化（原子写：tmp + rename）。
type KnownKeys struct {
	mu   sync.Mutex
	path string
	keys map[string]string
}

// LoadKnownKeys 加载或初始化 TOFU 表。
func LoadKnownKeys(path string) (*KnownKeys, error) {
	k := &KnownKeys{path: path, keys: map[string]string{}}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &k.keys); err != nil {
			return nil, fmt.Errorf("mesh: parse known keys %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("mesh: read known keys %s: %w", path, err)
	}
	return k, nil
}

// Trust 检查并记录 peerID 的公钥（TOFU）：
//   - 首次见到 → 记录并信任
//   - 与已记录一致 → 继续信任
//   - 不一致 → 返回错误（密钥轮换 / 中间人替换，拒绝）
func (k *KnownKeys) Trust(peerID string, pub []byte) error {
	want := base64.StdEncoding.EncodeToString(pub)
	k.mu.Lock()
	defer k.mu.Unlock()
	if got, ok := k.keys[peerID]; ok {
		if got != want {
			return fmt.Errorf("mesh: peer %s public key changed (TOFU violation)", peerID)
		}
		return nil
	}
	k.keys[peerID] = want
	return k.saveLocked()
}

// PeerKey 返回 peerID 的已信任公钥；未知返回 nil。
func (k *KnownKeys) PeerKey(peerID string) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	b64, ok := k.keys[peerID]
	if !ok {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	return raw
}

// saveLocked 原子持久化（调用方持锁）。
func (k *KnownKeys) saveLocked() error {
	if k.path == "" {
		return nil // 未配置持久化（纯内存模式）
	}
	if dir := filepath.Dir(k.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mesh: mkdir known keys dir: %w", err)
		}
	}
	raw, err := json.MarshalIndent(k.keys, "", "  ")
	if err != nil {
		return fmt.Errorf("mesh: marshal known keys: %w", err)
	}
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("mesh: write known keys tmp: %w", err)
	}
	if err := os.Rename(tmp, k.path); err != nil {
		return fmt.Errorf("mesh: rename known keys: %w", err)
	}
	return nil
}
