package core

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pandaymx/lanchat/internal/transfer"
)

const identityFileName = ".lanchat-identity.json"

type persistedIdentity struct {
	DeviceID   string `json:"deviceId"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

// desiredChannel 记录本机创建或加入的 G2 频道意图，供断线重连或 M14 房主切换后自愈（M18）。
type desiredChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	Private     bool   `json:"private"`
	CreatedByMe bool   `json:"createdByMe"`
}

// loadOrCreateIdentity 从 dir 加载或新建持久化 X25519 身份密钥（M15）。
// 当 dir 为空时，在内存中生成独立密钥对，保证无目录单测实例互不冲突。
func loadOrCreateIdentity(dir string) (deviceID string, priv *ecdh.PrivateKey, pubB64 string) {
	if dir != "" {
		path := filepath.Join(dir, identityFileName)
		if raw, err := os.ReadFile(path); err == nil {
			var p persistedIdentity
			if json.Unmarshal(raw, &p) == nil && p.PrivateKey != "" {
				if privBytes, err := base64.StdEncoding.DecodeString(p.PrivateKey); err == nil {
					if key, err := ecdh.X25519().NewPrivateKey(privBytes); err == nil {
						pub := transfer.EncodePub(key.PublicKey())
						id := deriveDeviceIDFromPub(key.PublicKey().Bytes())
						return id, key, pub
					}
				}
			}
		}
	}

	key, err := transfer.GenerateX25519()
	if err != nil {
		return "dev-fallback", nil, ""
	}
	pub := transfer.EncodePub(key.PublicKey())
	id := deriveDeviceIDFromPub(key.PublicKey().Bytes())

	if dir != "" {
		saveIdentity(dir, id, key, pub)
	}
	return id, key, pub
}

func saveIdentity(dir, deviceID string, priv *ecdh.PrivateKey, pubB64 string) {
	if dir == "" || priv == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	p := persistedIdentity{
		DeviceID:   deviceID,
		PrivateKey: base64.StdEncoding.EncodeToString(priv.Bytes()),
		PublicKey:  pubB64,
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, identityFileName), raw, 0o600)
}

// deriveDeviceIDFromPub 由公钥 SHA-256 前 16 个十六进制字符派生固定 DeviceID。
func deriveDeviceIDFromPub(pubBytes []byte) string {
	sum := sha256.Sum256(pubBytes)
	return "dev-" + hex.EncodeToString(sum[:8])
}

// IdentityFingerprint 根据 Base64 X25519 公钥生成可读安全码指纹（形如 A3F9-82C1-44D0）。
func IdentityFingerprint(pubB64 string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(raw) == 0 {
		return "0000-0000-0000"
	}
	sum := sha256.Sum256(raw)
	hx := strings.ToUpper(hex.EncodeToString(sum[:6]))
	return fmt.Sprintf("%s-%s-%s", hx[0:4], hx[4:8], hx[8:12])
}

// Fingerprint 返回本机静态身份公钥的可读安全码指纹（M15）。
func (c *Client) Fingerprint() string {
	c.mu.Lock()
	pub := c.identityPub
	c.mu.Unlock()
	return IdentityFingerprint(pub)
}

// PeerFingerprint 返回指定对端公钥的可读安全码指纹；对端无公钥时返回空串。
func (c *Client) PeerFingerprint(peerID string) string {
	c.mu.Lock()
	pub := c.peerPubs[peerID]
	c.mu.Unlock()
	if pub == "" {
		return ""
	}
	return IdentityFingerprint(pub)
}

// setChannelKey 记录指定 G2 频道的对称加密密钥（M19）。
func (c *Client) setChannelKey(channelID, keyB64 string) {
	if channelID == "" || keyB64 == "" {
		return
	}
	c.mu.Lock()
	c.channelKeys[channelID] = keyB64
	c.mu.Unlock()
}

// ensureChannelKey 获取或派生指定 G2 频道的对称加密密钥（M19）。
func (c *Client) ensureChannelKey(channelID string) string {
	if channelID == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if k := c.channelKeys[channelID]; k != "" {
		return k
	}
	if ch, ok := c.channels[channelID]; ok && ch.OwnerID == c.selfID && c.identityPriv != nil {
		if k, err := transfer.DeriveChannelKey(c.identityPriv, channelID); err == nil && k != "" {
			c.channelKeys[channelID] = k
			return k
		}
	}
	return ""
}
