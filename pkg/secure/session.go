package secure

// Package secure 提供 lanchat client↔hub 通道的传输加密原语。
//
// 信任模型与 mesh（ADR-014）一致：TOFU。hub 持有持久 X25519 身份
// （mesh_identity.bin 复用），客户端每次连接生成临时密钥对，通过
// 握手帧交换公钥后，用「静态-静态」ECDH → HKDF → AES-256-GCM 派生
// 会话密钥；客户端 TOFU 记录 hub 公钥，防中间人替换。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Challenge 是握手挑战明文：AES-GCM 密文由 hub 用会话密钥加密发出，
// 客户端解密后比对，证明 hub 确实拥有与临时公钥配对的持久私钥。
// 固定串即可——AEAD 认证保证完整性与真实性。
var Challenge = []byte("lanchat-ws-challenge-v1")

// Session 是一条 client↔hub 连接的单向/双向会话密钥。
//
// 由一方临时私钥 + 另一方持久公钥经 ECDH 派生（静态-静态，无状态），
// 同一对 (clientPriv, hubPub) 双方各自独立算出相同的 32B AES-256-GCM 密钥。
type Session struct {
	key [32]byte
	// aad 是 AEAD 附加认证数据：握手双方公钥排序拼接，绑定密钥对。
	aad []byte
}

// NewSession 由本端私钥与对端公钥派生会话密钥。
func NewSession(priv *ecdh.PrivateKey, pub []byte) (*Session, error) {
	peerPub, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("secure: peer public key: %w", err)
	}
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("secure: ecdh: %w", err)
	}
	salt := aadFor(priv.PublicKey().Bytes(), pub)
	key, err := hkdf.Key(sha256.New, shared, salt, "lanchat-ws-v1", 32)
	if err != nil {
		return nil, fmt.Errorf("secure: hkdf: %w", err)
	}
	s := &Session{aad: salt}
	copy(s.key[:], key)
	return s, nil
}

// aadFor 返回双方公钥排序拼接的认证数据（与 mesh 的 salt 约定一致）。
func aadFor(pubA, pubB []byte) []byte {
	out := make([]byte, 0, 64)
	if string(pubA) <= string(pubB) {
		out = append(out, pubA...)
		out = append(out, pubB...)
	} else {
		out = append(out, pubB...)
		out = append(out, pubA...)
	}
	return out
}

// Seal 用 AES-256-GCM 加密明文，输出 wire 上的 Envelope 形式：
// {"n": base64(nonce), "c": base64(密文含 tag)}。nonce 每帧随机 12B。
func (s *Session) Seal(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secure: nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plain, s.aad)
	return json.Marshal(struct {
		N string `json:"n"`
		C string `json:"c"`
	}{base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ciphertext)})
}

// Open 解密 Seal 的输出。
func (s *Session) Open(data []byte) ([]byte, error) {
	var env struct {
		N string `json:"n"`
		C string `json:"c"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("secure: envelope: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.N)
	if err != nil {
		return nil, fmt.Errorf("secure: nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.C)
	if err != nil {
		return nil, fmt.Errorf("secure: cipher: %w", err)
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, s.aad)
	if err != nil {
		return nil, fmt.Errorf("secure: decrypt: %w", err)
	}
	return plain, nil
}

// SealChallenge 加密握手挑战（hub → 客户端，证明持有私钥）。
func (s *Session) SealChallenge() (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secure: nonce: %w", err)
	}
	return nonce, aead.Seal(nil, nonce, Challenge, s.aad), nil
}

// VerifyChallenge 校验 hub 返回的挑战密文。
func (s *Session) VerifyChallenge(nonce, ciphertext []byte) error {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, s.aad)
	if err != nil {
		return fmt.Errorf("secure: challenge verify: %w", err)
	}
	if string(plain) != string(Challenge) {
		return errors.New("secure: challenge mismatch")
	}
	return nil
}

// Key 返回会话密钥（测试用）。
func (s *Session) Key() []byte { return s.key[:] }

// AAD 返回 AEAD 认证数据（测试用）。
func (s *Session) AAD() []byte { return s.aad }
