// Package secure 提供 lanchat client↔hub 通道的传输加密原语。
// 详细说明见 session.go（会话密钥 / TOFU 信任存储 / 便捷函数）。
package secure

import (
	"fmt"
)

// session.go 补充：从已有 key/aad 构造 Session（ws.conn 加解密用，
// 避免每次 Seal/Open 重复存状态）。

// NewSessionFromKey 用已派生的会话密钥与 AAD 构造 Session。
// key 必须 32B；aad 是握手双方公钥排序拼接（服务端/客户端各自持久）。
func NewSessionFromKey(key, aad []byte) (*Session, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secure: session key must be 32 bytes, got %d", len(key))
	}
	s := &Session{aad: append([]byte(nil), aad...)}
	copy(s.key[:], key)
	return s, nil
}
