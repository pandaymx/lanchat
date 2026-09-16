package mesh

// handshake.go 是 mesh 节点的身份握手与配对授权（gated TOFU）。
//
// 背景：wire v2 的信任模型是「按来源 IP 的纯 TOFU」——局域网里任何
// 一台机器首次发来加密请求就会被自动信任，也没有设备身份概念。本文件
// 把信任升级为「密钥握手 + Token 配对」：
//
//   - 节点身份 = X25519 公钥指纹（PeerID）：稳定、换 IP 不丢信任、
//     无法冒充；设备名（人类可读）随握手记录，不再用 IP 当 peerID。
//   - 已知节点（密钥已入 KnownKeys）→ 密钥握手直接通过，无需 Token。
//   - 未知节点 → 必须携带正确配对 Token 才被授权加入；未配置 Token
//     时保持旧 TOFU 行为（向后兼容）。
//
// Token 是 32B 随机数，base32 编码成 52 字符可读串，带外分享给新设备；
// 服务端只保存 Token 的 SHA-256 哈希。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrUnauthorized 表示握手被拒：未知节点未携带有效配对 Token。
// 服务端映射为 HTTP 403。
var ErrUnauthorized = errors.New("mesh: peer not authorized (missing or invalid join token)")

// peerIDLen 是 PeerID 指纹的字节数（sha256 前 16B，32 hex 字符）。
const peerIDLen = 16

// NewToken 生成一个配对 Token（32B 随机 → base32 无填充，52 字符）。
// 生成的 Token 打印给管理员，由管理员带外分享给要加入的新设备。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// HashToken 返回 Token 的 SHA-256 hex（服务端仅存哈希，防泄库冒充）。
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// PeerID 由节点公钥派生稳定身份指纹：sha256(pub) 前 16B 的 hex。
// 密钥即身份——同一设备换 IP 后 PeerID 不变，信任记录不漂移。
func PeerID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:peerIDLen])
}

// authorizePeer 是服务端统一握手鉴权：
//
//   - 已知 peerID：密钥握手通过；若请求带设备名则更新记录。
//   - 未知 peerID：
//   - joinHash 为空（未配置配对）→ 保持旧 TOFU：直接信任并记录。
//   - joinHash 非空 → 校验请求 Token 的哈希；匹配才授权加入，
//     否则返回 ErrUnauthorized（拒绝，不记录）。
func (k *KnownKeys) authorizePeer(joinHash, peerPub []byte, device, token string) (string, error) {
	peerID := PeerID(peerPub)
	if k.IsKnown(peerID) {
		if device != "" {
			_ = k.SetDevice(peerID, device)
		}
		return peerID, nil
	}
	// 未配置配对 Token：保持向后兼容的纯 TOFU（记录并信任）。
	if len(joinHash) == 0 {
		if err := k.Trust(peerID, peerPub); err != nil {
			return "", err
		}
		if device != "" {
			_ = k.SetDevice(peerID, device)
		}
		return peerID, nil
	}
	// 配置了配对 Token：未知节点必须证明被邀请。
	tok := strings.TrimSpace(token)
	if tok == "" || !constantTimeEq(HashToken(tok), string(joinHash)) {
		return "", ErrUnauthorized
	}
	if err := k.Trust(peerID, peerPub); err != nil {
		return "", err
	}
	if device != "" {
		_ = k.SetDevice(peerID, device)
	}
	return peerID, nil
}

// constantTimeEq 常量时间比较两个 hex 串（防时序侧信道）。
func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
