package e2e

// envelope.go 是 E2E 消息信封（ECIES，每消息随机 DEK）。
//
// 格式（v1）：
//
//	eph       临时 X25519 公钥（每次加密新生成）
//	recipients 接收者列表：keyID(公钥指纹) + encDEK(AES-GCM 封装的数据密钥)
//	           + dekNonce；单聊一个、群聊每成员一个，DEK 共享
//	ct        正文密文：AES-256-GCM(DEK, plaintext)，nonce 附在信封
//
// 派生链：ephPriv × recvPub → ECDH 共享秘密 → HKDF-SHA256(salt=排序
// 公钥, info="lanchat-e2e-v1") → 32B 封装密钥 → AES-GCM 封装 DEK；
// DEK 直接 AES-GCM 加密正文。服务端/中继只见过信封与密文，看不到
// 明文，也无法在没有任一方私钥时解出 DEK。

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

const (
	// EnvelopeVersion 是当前信封版本。
	EnvelopeVersion = 1
	// keyInfo 是 DEK 封装密钥的 HKDF info 常量（区分用途）。
	keyInfo = "lanchat-e2e-v1"
	// maxPlain 防止巨型明文导致内存放大（正文上限，64MiB）。
	maxPlain = 64 << 20
)

// Recipient 是单个接收者的 DEK 封装。
type Recipient struct {
	KeyID    string `json:"key"` // 接收者 E2E 公钥指纹（PeerID）
	EncDEK   []byte `json:"dek"` // AES-GCM(封装密钥, DEK)
	DEKNonce []byte `json:"dn"`  // 封装用的 GCM nonce
}

// Envelope 是一条 E2E 消息的密文信封。
type Envelope struct {
	Version    int         `json:"v"`
	Ephemeral  []byte      `json:"eph"` // 临时 X25519 公钥（raw 32B）
	Recipients []Recipient `json:"rcpts"`
	Nonce      []byte      `json:"n"` // 正文 GCM nonce
	Ciphertext []byte      `json:"ct"`
}

// Encrypt 用接收者公钥加密明文，返回单接收者信封。
func Encrypt(recvPub []byte, plaintext []byte) (*Envelope, error) {
	return EncryptMulti([][]byte{recvPub}, plaintext)
}

// EncryptMulti 用一组接收者公钥加密明文（群聊：DEK 共享，逐成员封装）。
//
// 至少一个接收者；任一公钥非法即整体失败（不产生半加密消息）。
// plaintext 长度受 maxPlain 限制。
func EncryptMulti(recvPubs [][]byte, plaintext []byte) (*Envelope, error) {
	if len(recvPubs) == 0 {
		return nil, errors.New("e2e: no recipients")
	}
	if len(plaintext) > maxPlain {
		return nil, fmt.Errorf("e2e: plaintext too large (%d > %d)", len(plaintext), maxPlain)
	}
	// 1) 随机数据密钥 DEK（正文加密键）。
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("e2e: rand dek: %w", err)
	}
	// 2) 临时 eph 密钥对（每条消息新生成）。
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("e2e: gen ephemeral: %w", err)
	}
	// 3) 正文 AES-256-GCM。
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("e2e: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("e2e: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("e2e: rand nonce: %w", err)
	}
	env := &Envelope{
		Version:    EnvelopeVersion,
		Ephemeral:  eph.PublicKey().Bytes(),
		Nonce:      nonce,
		Ciphertext: gcm.Seal(nil, nonce, plaintext, nil),
	}
	// 4) 每个接收者封装 DEK。
	seen := map[string]bool{}
	for _, pub := range recvPubs {
		keyID := PeerID(pub)
		if seen[keyID] {
			continue // 同一接收者只封装一次
		}
		seen[keyID] = true
		rcpt, err := wrapDEK(eph, pub, dek)
		if err != nil {
			return nil, err
		}
		env.Recipients = append(env.Recipients, rcpt)
	}
	return env, nil
}

// wrapDEK 用 eph 私钥 × 接收者公钥派生封装密钥，AES-GCM 封装 DEK。
func wrapDEK(eph *ecdh.PrivateKey, recvPub, dek []byte) (Recipient, error) {
	pub, err := ecdh.X25519().NewPublicKey(recvPub)
	if err != nil {
		return Recipient{}, fmt.Errorf("e2e: invalid recipient key (%d bytes): %w", len(recvPub), err)
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return Recipient{}, fmt.Errorf("e2e: ecdh: %w", err)
	}
	key := deriveKey(shared, eph.PublicKey().Bytes(), recvPub)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Recipient{}, fmt.Errorf("e2e: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Recipient{}, fmt.Errorf("e2e: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Recipient{}, fmt.Errorf("e2e: rand: %w", err)
	}
	return Recipient{
		KeyID:    PeerID(recvPub),
		EncDEK:   gcm.Seal(nil, nonce, dek, nil),
		DEKNonce: nonce,
	}, nil
}

// Decrypt 用本节点 E2E 私钥解出明文。
//
// 在 recipients 中按 KeyID 匹配本节点指纹，解出 DEK 再解正文。
// 非接收者（无对应封装）返回 ErrNotRecipient；认证失败（密文被篡改
// 或密钥不匹配）返回 ErrDecrypt。
var (
	// ErrNotRecipient 信封里没有本节点的 DEK 封装。
	ErrNotRecipient = errors.New("e2e: not a recipient of this message")
	// ErrDecrypt 解密/认证失败（密钥不匹配或密文被篡改）。
	ErrDecrypt = errors.New("e2e: decrypt failed")
)

// Decrypt 用本节点 E2E 私钥解出明文信封内容。
func (e *Envelope) Decrypt(priv *ecdh.PrivateKey) ([]byte, error) {
	if e == nil || e.Version != EnvelopeVersion {
		return nil, ErrDecrypt
	}
	myID := PeerID(priv.PublicKey().Bytes())
	ephPub, err := ecdh.X25519().NewPublicKey(e.Ephemeral)
	if err != nil {
		return nil, ErrDecrypt
	}
	for _, rcpt := range e.Recipients {
		if rcpt.KeyID != myID {
			continue
		}
		shared, err := priv.ECDH(ephPub)
		if err != nil {
			return nil, ErrDecrypt
		}
		key := deriveKey(shared, e.Ephemeral, priv.PublicKey().Bytes())
		dek, err := openGCM(key, rcpt.DEKNonce, rcpt.EncDEK)
		if err != nil {
			return nil, ErrDecrypt
		}
		plain, err := openGCM(dek, e.Nonce, e.Ciphertext)
		if err != nil {
			return nil, ErrDecrypt
		}
		return plain, nil
	}
	return nil, ErrNotRecipient
}

// openGCM 是 AES-GCM 解封的公共路径（认证失败统一返回错误）。
func openGCM(key, nonce, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("e2e: bad nonce size")
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// deriveKey 从 ECDH 共享秘密派生 DEK 封装密钥。
// salt 固定为双方公钥排序拼接（与 mesh 同模式，防密钥混淆），
// info 固定 "lanchat-e2e-v1" 区分用途。
func deriveKey(shared, pubA, pubB []byte) []byte {
	salt := make([]byte, 0, len(pubA)+len(pubB))
	if string(pubA) <= string(pubB) {
		salt = append(salt, pubA...)
		salt = append(salt, pubB...)
	} else {
		salt = append(salt, pubB...)
		salt = append(salt, pubA...)
	}
	key, err := hkdf.Key(sha256.New, shared, salt, keyInfo, 32)
	if err != nil {
		panic("e2e: hkdf derive failed: " + err.Error())
	}
	return key
}

// Marshal 序列化信封（base64 字符串，便于放入 StoredMessage 字段与 JSON）。
func (e *Envelope) Marshal() (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("e2e: marshal envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Unmarshal 反序列化信封。
func Unmarshal(s string) (*Envelope, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("e2e: decode envelope: %w", err)
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("e2e: unmarshal envelope: %w", err)
	}
	if e.Version != EnvelopeVersion {
		return nil, fmt.Errorf("e2e: unsupported envelope version %d", e.Version)
	}
	return &e, nil
}
