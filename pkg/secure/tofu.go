package secure

// tofu.go 是客户端对 hub 公钥的 TOFU 信任存储：
// peerID（hub 的 host:port）→ 公钥，JSON 持久化（tmp+rename 原子写）。
//
// 规则：首次信任、同钥放行、轮换拒绝（与 mesh.KnownKeys 语义一致）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrKeyChanged 是 TOFU 冲突：同一 peer 换了公钥。
var ErrKeyChanged = errors.New("secure: peer public key changed (TOFU violation)")

// TrustStore 是持久化的 TOFU 公钥表。
type TrustStore struct {
	mu   sync.Mutex
	path string
	keys map[string][]byte // peerID → hub 公钥
}

// LoadTrustStore 加载（或创建）一个 TOFU 存储。
// 文件不存在时返回空表（首次连接会写入）。
func LoadTrustStore(path string) (*TrustStore, error) {
	ts := &TrustStore{path: path, keys: map[string][]byte{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ts, nil
		}
		return nil, fmt.Errorf("secure: read trust store: %w", err)
	}
	if err := json.Unmarshal(raw, &ts.keys); err != nil {
		return nil, fmt.Errorf("secure: parse trust store: %w", err)
	}
	return ts, nil
}

// Trust 记录/校验 peer 的公钥。首次信任返回 nil；同钥幂等；轮换拒绝。
func (t *TrustStore) Trust(peerID string, pub []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if got, ok := t.keys[peerID]; ok {
		if string(got) != string(pub) {
			return ErrKeyChanged
		}
		return nil
	}
	// 首次信任：内存更新 + 原子落盘。
	t.keys[peerID] = append([]byte(nil), pub...)
	return t.persistLocked()
}

// Verify 校验 peer 公钥是否可信：未记录 → 首次信任并返回 nil；
// 已记录且一致 → nil；不一致 → ErrKeyChanged。
func (t *TrustStore) Verify(peerID string, pub []byte) error {
	return t.Trust(peerID, pub)
}

// PubOf 返回 peer 已记录的公钥；未记录返回 nil。
func (t *TrustStore) PubOf(peerID string) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.keys[peerID]...)
}

// Path 返回存储文件路径（测试用）。
func (t *TrustStore) Path() string { return t.path }

// persistLocked 把 keys 原子写入文件（tmp + rename，0600）。
func (t *TrustStore) persistLocked() error {
	if t.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return fmt.Errorf("secure: trust store dir: %w", err)
	}
	raw, err := json.Marshal(t.keys)
	if err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("secure: trust store write: %w", err)
	}
	return os.Rename(tmp, t.path)
}
