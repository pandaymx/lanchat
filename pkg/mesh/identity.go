package mesh

// identity.go 是 mesh 节点的静态身份密钥（X25519）。
//
// 每个节点持久化一把 X25519 私钥（raw 32B 文件，0600），公钥作为
// 节点在 mesh 中的身份标识。节点间加密用「静态-静态 ECDH」：
// 双方私钥+对方公钥 → 共享密钥 → HKDF 派生 AES-256-GCM 会话密钥，
// 无状态（每请求可算），无需握手轮次。

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Identity 是一个节点的 X25519 身份。
type Identity struct {
	priv *ecdh.PrivateKey
}

// LoadOrCreateIdentity 从 path 加载或创建节点身份。
//
// 不存在时生成新密钥并以 0600 权限持久化（私钥明文在磁盘——本机
// 文件权限即防线；mesh 信任模型不设口令，见 ADR-014）。创建后不可
// 随意更换：公钥是节点身份，(NodeID, ServerSeq) 坐标与 TOFU 记录
// 都依赖它稳定。
func LoadOrCreateIdentity(path string) (*Identity, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if len(raw) == 32 {
			priv, err := ecdh.X25519().NewPrivateKey(raw)
			if err == nil {
				return &Identity{priv: priv}, nil
			}
		}
		return nil, fmt.Errorf("mesh: identity file %s invalid (len=%d)", path, len(raw))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("mesh: read identity %s: %w", path, err)
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mesh: generate identity: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mesh: mkdir identity dir: %w", err)
		}
	}
	if err := os.WriteFile(path, priv.Bytes(), 0o600); err != nil {
		return nil, fmt.Errorf("mesh: persist identity: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// PublicKey 返回本节点公钥（raw 32B）。
func (i *Identity) PublicKey() []byte { return i.priv.PublicKey().Bytes() }

// SharedKey 计算本节点与 peerPub 的 ECDH 共享密钥（32B）。
func (i *Identity) SharedKey(peerPub []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("mesh: invalid peer public key (%d bytes): %w", len(peerPub), err)
	}
	return i.priv.ECDH(pub)
}

// deriveKey 用 HKDF-SHA256 从 ECDH 共享密钥派生 AES-256-GCM 会话密钥。
//
// salt 固定为双方公钥排序拼接——同一对节点得到确定且对称的派生，
// 注入方向信息防止密钥混淆。info 区分用途（版本标识）。
func deriveKey(shared []byte, pubA, pubB []byte) []byte {
	salt := make([]byte, 0, 64)
	if string(pubA) <= string(pubB) {
		salt = append(salt, pubA...)
		salt = append(salt, pubB...)
	} else {
		salt = append(salt, pubB...)
		salt = append(salt, pubA...)
	}
	key, err := hkdf.Key(sha256.New, shared, salt, "lanchat-mesh-v1", 32)
	if err != nil {
		// 输入长度固定（32B ECDH 输出），HKDF 只会因长度溢出失败，不会到这。
		panic("mesh: hkdf derive failed: " + err.Error())
	}
	return key
}
