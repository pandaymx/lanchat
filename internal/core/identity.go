package core

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/internal/transfer"
)

const (
	identityFileName = ".lanchat-identity.json"
	keyDirFileName   = ".lanchat-keydir.json"
)

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

func loadKeyDirectory(dir string) map[string]string {
	out := make(map[string]string)
	if strings.TrimSpace(dir) == "" {
		return out
	}
	raw, err := os.ReadFile(filepath.Join(dir, keyDirFileName))
	if err != nil || len(raw) == 0 {
		return out
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				out[k] = v
			}
		}
	}
	return out
}

func saveKeyDirectory(dir string, keyDir map[string]string) {
	if strings.TrimSpace(dir) == "" || len(keyDir) == 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	raw, err := json.MarshalIndent(keyDir, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, keyDirFileName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(dir, keyDirFileName))
}

func (c *Client) mergePeerPubsLocked(entries map[string]string) bool {
	if c.peerPubs == nil {
		c.peerPubs = make(map[string]string)
	}
	changed := false
	for id, pub := range entries {
		id = strings.TrimSpace(id)
		pub = strings.TrimSpace(pub)
		if id == "" || pub == "" || id == c.selfID || id == c.deviceID {
			continue
		}
		if c.peerPubs[id] != pub {
			c.peerPubs[id] = pub
			changed = true
		}
	}
	return changed
}

func (c *Client) persistKeyDirectory() {
	c.mu.Lock()
	dir := c.downloadDir
	if strings.TrimSpace(dir) == "" || len(c.peerPubs) == 0 {
		c.mu.Unlock()
		return
	}
	snap := make(map[string]string, len(c.peerPubs))
	for k, v := range c.peerPubs {
		if k != "" && v != "" {
			snap[k] = v
		}
	}
	c.mu.Unlock()
	saveKeyDirectory(dir, snap)
}

// KeyDirectory 返回当前客户端缓存的全网节点 X25519 公钥目录快照（含本机公钥，v8.1.0）。
func (c *Client) KeyDirectory() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.peerPubs)+1)
	for k, v := range c.peerPubs {
		if k != "" && v != "" {
			out[k] = v
		}
	}
	selfID := c.selfID
	if selfID == "" {
		selfID = c.deviceID
	}
	if selfID != "" && c.identityPub != "" {
		out[selfID] = c.identityPub
	}
	return out
}

func (c *Client) handleWelcomeKeyDirectory(features []string) {
	for _, f := range features {
		if !strings.HasPrefix(f, "keydir:") {
			continue
		}
		b64 := strings.TrimSpace(strings.TrimPrefix(f, "keydir:"))
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) == 0 {
			continue
		}
		var kd map[string]string
		if json.Unmarshal(raw, &kd) != nil || len(kd) == 0 {
			continue
		}
		c.mu.Lock()
		changed := c.mergePeerPubsLocked(kd)
		c.mu.Unlock()
		if changed {
			c.persistKeyDirectory()
		}
	}
}

func (c *Client) onInboundKeyDirectory(b64 string) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) == 0 {
		return
	}
	var kd map[string]string
	if json.Unmarshal(raw, &kd) != nil || len(kd) == 0 {
		return
	}
	c.mu.Lock()
	changed := c.mergePeerPubsLocked(kd)
	c.mu.Unlock()
	if changed {
		c.persistKeyDirectory()
	}
}

func (c *Client) broadcastKeyDirectoryToPeers(peerIDs []string) {
	if len(peerIDs) == 0 {
		return
	}
	kd := c.KeyDirectory()
	if len(kd) == 0 {
		return
	}
	raw, err := json.Marshal(kd)
	if err != nil {
		return
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	for _, pid := range peerIDs {
		pid = strings.TrimSpace(pid)
		if pid == "" {
			continue
		}
		_ = c.sendControlText(pid, "", "sys:keydir:"+b64)
	}
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

// DeviceID 返回本机静态身份公钥派生的设备标识（M15）。
func (c *Client) DeviceID() string {
	c.mu.Lock()
	id := c.deviceID
	c.mu.Unlock()
	return id
}

// IdentityPublicKey 返回本机静态 X25519 公钥（Base64 编码，v8.0.0）。
func (c *Client) IdentityPublicKey() string {
	c.mu.Lock()
	pub := c.identityPub
	c.mu.Unlock()
	return pub
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

// nodeCertClaims 表示 v4.0.0 零信任企业节点准入证书载荷。
type nodeCertClaims struct {
	SubjectID string `json:"sub"`
	Org       string `json:"org"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func (c *Client) deriveEd25519SigningKey() (ed25519.PrivateKey, ed25519.PublicKey) {
	c.mu.Lock()
	priv := c.identityPriv
	devID := c.deviceID
	c.mu.Unlock()
	var seed [32]byte
	if priv != nil {
		seed = sha256.Sum256(priv.Bytes())
	} else {
		seed = sha256.Sum256([]byte("lanchat-ed25519-" + devID))
	}
	edPriv := ed25519.NewKeyFromSeed(seed[:])
	edPub := edPriv.Public().(ed25519.PublicKey)
	return edPriv, edPub
}

// IssueNodeCertificate 使用本机派生的 Ed25519 根私钥为指定设备签发组织准入证书（v4.0.0）。
func (c *Client) IssueNodeCertificate(subjectDeviceID, orgName string, ttlSec int64) (token string, rootPubB64 string, err error) {
	subjectDeviceID = strings.TrimSpace(subjectDeviceID)
	if subjectDeviceID == "" {
		subjectDeviceID = c.DeviceID()
	}
	orgName = strings.TrimSpace(orgName)
	if orgName == "" {
		orgName = "LANChat-Enterprise"
	}
	if ttlSec <= 0 {
		ttlSec = 86400 * 30
	}
	now := time.Now().Unix()
	claims := nodeCertClaims{
		SubjectID: subjectDeviceID,
		Org:       orgName,
		IssuedAt:  now,
		ExpiresAt: now + ttlSec,
	}
	payloadRaw, err := json.Marshal(claims)
	if err != nil {
		return "", "", err
	}
	edPriv, edPub := c.deriveEd25519SigningKey()
	sig := ed25519.Sign(edPriv, payloadRaw)
	token = base64.RawURLEncoding.EncodeToString(payloadRaw) + "." + base64.RawURLEncoding.EncodeToString(sig)
	rootPubB64 = base64.StdEncoding.EncodeToString(edPub)
	return token, rootPubB64, nil
}

// VerifyNodeCertificate 校验 Ed25519 组织准入证书签名与有效期（v4.0.0）。
func VerifyNodeCertificate(token, rootPubB64 string) (subjectDeviceID, orgName string, ok bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return "", "", false
	}
	payloadRaw, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	pubRaw, err3 := base64.StdEncoding.DecodeString(strings.TrimSpace(rootPubB64))
	if err1 != nil || err2 != nil || err3 != nil || len(pubRaw) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return "", "", false
	}
	if !ed25519.Verify(ed25519.PublicKey(pubRaw), payloadRaw, sig) {
		return "", "", false
	}
	var claims nodeCertClaims
	if json.Unmarshal(payloadRaw, &claims) != nil || claims.SubjectID == "" {
		return "", "", false
	}
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return "", "", false
	}
	return claims.SubjectID, claims.Org, true
}
