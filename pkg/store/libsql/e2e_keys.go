package libsql

// e2e_keys.go 是设备 E2E 公钥目录（keyring）的持久化实现。
//
// 与消息表分离：公钥不随消息本体落库（消息里带 E2EKey 自我声明，
// 落库时提取进这里），避免给消息表加列、也便于独立查询。

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SaveE2EKey 记录设备 E2E 公钥（幂等覆盖，刷新 updated_at）。
func (s *Store) SaveE2EKey(ctx context.Context, deviceID, pubkey string) error {
	if deviceID == "" {
		return fmt.Errorf("libsql: e2e key device id empty")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO e2e_keys (device_id, pubkey, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET
		   pubkey = excluded.pubkey,
		   updated_at = excluded.updated_at`,
		deviceID, pubkey, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("libsql: save e2e key %q: %w", deviceID, err)
	}
	return nil
}

// DeleteE2EKey 从 keyring 吊销设备公钥（幂等）。
func (s *Store) DeleteE2EKey(ctx context.Context, deviceID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM e2e_keys WHERE device_id = ?`, deviceID); err != nil {
		return fmt.Errorf("libsql: delete e2e key %q: %w", deviceID, err)
	}
	return nil
}

// GetE2EPinnedKey 取本地 TOFU pin；未 pin 返回空串。
func (s *Store) GetE2EPinnedKey(ctx context.Context, deviceID string) (string, error) {
	var pub string
	err := s.db.QueryRowContext(ctx,
		`SELECT pubkey FROM e2e_pins WHERE device_id = ?`, deviceID).Scan(&pub)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("libsql: get e2e pin %q: %w", deviceID, err)
	}
	return pub, nil
}

// SaveE2EPinnedKey 记录/更新 pin（幂等）。
func (s *Store) SaveE2EPinnedKey(ctx context.Context, deviceID, pubkey string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO e2e_pins (device_id, pubkey, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET
		   pubkey = excluded.pubkey,
		   updated_at = excluded.updated_at`,
		deviceID, pubkey, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("libsql: save e2e pin %q: %w", deviceID, err)
	}
	return nil
}

// GetE2EKey 查询设备 E2E 公钥；未记录返回空串（非错误）。
func (s *Store) GetE2EKey(ctx context.Context, deviceID string) (string, error) {
	var pub string
	err := s.db.QueryRowContext(ctx,
		`SELECT pubkey FROM e2e_keys WHERE device_id = ?`, deviceID).Scan(&pub)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("libsql: get e2e key %q: %w", deviceID, err)
	}
	return pub, nil
}
