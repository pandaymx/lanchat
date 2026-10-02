// 本文件实现中继会话的端到端密码学：X25519 临时密钥、ECDH + HKDF 派生、
// 以及对一次性文件密钥的 AES-GCM 信封封装。
//
// 威胁模型：中继/信令服务器不可信，只允许看到密文。文件密钥由接收方生成，
// 用双方 X25519 ECDH 派生的 KEK 加密后回传，中心节点无法还原文件密钥。
package transfer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
)

// hkdfInfo 是 KEK 派生的上下文绑定信息，防止跨协议复用。
const hkdfInfo = "lanchat/relay-kek/v1"

// groupConnInfo 是群文件块连接密钥派生的上下文绑定信息。
const groupConnInfo = "lanchat/group-conn/v1"

// generateX25519 生成一对一次性 X25519 密钥。
func generateX25519() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// encodePub 把 X25519 公钥编码为 base64（用于信令字段）。
func encodePub(pub *ecdh.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub.Bytes())
}

// decodePub 解析 base64 编码的 X25519 公钥。
func decodePub(s string) (*ecdh.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("transfer: decode pub: %w", err)
	}
	return ecdh.X25519().NewPublicKey(raw)
}

// deriveKEK 用本地临时私钥与对端公钥做 ECDH，再 HKDF-SHA256 派生 32 字节 KEK。
func deriveKEK(priv *ecdh.PrivateKey, peerPub *ecdh.PublicKey) ([]byte, error) {
	secret, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("transfer: ecdh: %w", err)
	}
	kek, err := hkdf.Key(sha256.New, secret, nil, hkdfInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("transfer: hkdf: %w", err)
	}
	return kek, nil
}

// generateFileKey 生成一次性 32 字节 AES-256 文件密钥。
func generateFileKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("transfer: gen file key: %w", err)
	}
	return key, nil
}

// wrapKey 用 KEK 对文件密钥做 AES-GCM 加密封装，返回 nonce||ciphertext 的 base64。
func wrapKey(kek, fileKey []byte) (string, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("transfer: gen nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, fileKey, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// unwrapKey 用 KEK 解密 wrapKey 产出的信封，还原文件密钥。
func unwrapKey(kek []byte, wrapped string) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(wrapped)
	if err != nil {
		return nil, fmt.Errorf("transfer: decode wrapped key: %w", err)
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("transfer: wrapped key too short")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	fileKey, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("transfer: unwrap key: %w", err)
	}
	return fileKey, nil
}

// 以下导出函数供客户端编排与集成测试使用，语义与对应小写内部函数一致。

// GenerateX25519 生成一对一次性 X25519 密钥。
func GenerateX25519() (*ecdh.PrivateKey, error) { return generateX25519() }

// EncodePub 把 X25519 公钥编码为 base64。
func EncodePub(pub *ecdh.PublicKey) string { return encodePub(pub) }

// DecodePub 解析 base64 编码的 X25519 公钥。
func DecodePub(s string) (*ecdh.PublicKey, error) { return decodePub(s) }

// DeriveKEK 用本地临时私钥与对端公钥做 ECDH + HKDF 派生 32B KEK。
func DeriveKEK(priv *ecdh.PrivateKey, peerPub *ecdh.PublicKey) ([]byte, error) {
	return deriveKEK(priv, peerPub)
}

// GenerateFileKey 生成一次性 32B AES-256 文件密钥。
func GenerateFileKey() ([]byte, error) { return generateFileKey() }

// WrapKey 用 KEK 对文件密钥做 AES-GCM 信封封装。
func WrapKey(kek, fileKey []byte) (string, error) { return wrapKey(kek, fileKey) }

// UnwrapKey 用 KEK 解密信封还原文件密钥。
func UnwrapKey(kek []byte, wrapped string) ([]byte, error) { return unwrapKey(kek, wrapped) }

// DeriveGroupConnKey 为一条群文件块连接派生独立的 AES-256 连接密钥。
//
// 派生输入 = ECDH(priv, peerPub)（每连接一次性临时 X25519）+ salt=fileKey
// （本次群文件对称密钥）。把 fileKey 混入 HKDF salt 的作用：
//   - 每条连接拿到独立密钥，避免多连接共用 fileKey 时 GCM nonce 冲突；
//   - 被动观察者拿不到 fileKey，即便记录全部流量也无法推导连接密钥；
//   - 中间人（含被攻陷的中继）即使替换 HELLO 中的临时公钥，因双方算出的
//     ECDH 值都被各自 fileKey 绑定，其无法完成两边一致的 MITM。
func DeriveGroupConnKey(priv *ecdh.PrivateKey, peerPub *ecdh.PublicKey, fileKey []byte) ([]byte, error) {
	secret, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("transfer: group ecdh: %w", err)
	}
	key, err := hkdf.Key(sha256.New, secret, fileKey, groupConnInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("transfer: group hkdf: %w", err)
	}
	return key, nil
}

// NewEncConn 用给定密钥在 raw 之上构造透明 AES-GCM 加密连接（net.Conn）。
func NewEncConn(raw net.Conn, key []byte) (net.Conn, error) { return newEncConn(raw, key) }

// newGCM 以 32 字节密钥构造 AES-256-GCM。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("transfer: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("transfer: gcm: %w", err)
	}
	return gcm, nil
}
