package e2e

// identity.go 是端到端加密（E2E）的静态身份密钥（X25519）。
//
// 与 mesh 传输身份分层：mesh_identity.bin 加密「通道」（传输层），
// e2e_identity.bin 加密「内容」（消息正文）。即使传输密钥泄露，
// 历史消息仍只有持 E2E 私钥的接收端能解。每条消息用临时 eph 密钥
// 做 ECDH（前向保密性有限但无状态、无需握手轮次），随机 DEK 封装
// 密文。持久化模式与 pkg/mesh/identity.go 一致（raw 32B, 0600）。

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Identity 是一个节点的 E2E 身份（X25519 密钥对）。
type Identity struct {
	priv *ecdh.PrivateKey
}

// LoadOrCreateIdentity 从 path 加载或创建 E2E 身份。
//
// 不存在时生成并 0600 持久化。创建后不可随意更换：公钥是接收者
// 的 E2E 身份，历史消息信封都向它封装，换钥即丢可解密能力。
func LoadOrCreateIdentity(path string) (*Identity, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if len(raw) == 32 {
			priv, err := ecdh.X25519().NewPrivateKey(raw)
			if err == nil {
				return &Identity{priv: priv}, nil
			}
		}
		return nil, fmt.Errorf("e2e: identity file %s invalid (len=%d)", path, len(raw))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("e2e: read identity %s: %w", path, err)
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("e2e: generate identity: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("e2e: mkdir identity dir: %w", err)
		}
	}
	if err := os.WriteFile(path, priv.Bytes(), 0o600); err != nil {
		return nil, fmt.Errorf("e2e: persist identity: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// PublicKey 返回本节点 E2E 公钥（raw 32B）。
func (i *Identity) PublicKey() []byte { return i.priv.PublicKey().Bytes() }

// PrivateKey 返回底层 X25519 私钥，供解密与 ECDH 使用。
// 调用方不得修改返回对象。
func (i *Identity) PrivateKey() *ecdh.PrivateKey { return i.priv }

// PeerID 返回公钥指纹（sha256(pub)[:16] 的 hex，32 字符），
// 与 mesh.PeerID 同规则：换 IP 不丢身份、密钥目录按它索引。
func PeerID(pub []byte) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:16])
}
