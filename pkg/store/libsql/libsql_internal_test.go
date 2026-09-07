package libsql

// 包内测试：迁移路径需要直接操作底层 db（DROP COLUMN 模拟老库），
// 外部包测试拿不到私有字段，故放本包内。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// TestMigration_AddsFileColumnsToLegacyDB 模拟老库（无附件列）：
// 先建库再 DROP 附件列，reopen 触发 M9 迁移，确认幂等补列 + 附件读写正常。
func TestMigration_AddsFileColumnsToLegacyDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, col := range []string{"file_id", "file_name", "file_size", "file_mime"} {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE messages DROP COLUMN `+col); err != nil {
			t.Fatalf("drop column %s: %v", col, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重开触发迁移：幂等补列，不应报 duplicate column。
	s2, err := Open(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if err := s2.AppendMessage(ctx, protocol.StoredMessage{
		ID: "m1", ConversationID: "lobby", Body: "", ServerSeq: 1, CreatedAt: 1,
		File: &protocol.FileRef{FileID: "f1", Name: "a.bin", Size: 5, Mime: "application/octet-stream"},
	}); err != nil {
		t.Fatalf("AppendMessage after migration: %v", err)
	}
	msgs, err := s2.History(ctx, "lobby", 0, 0)
	if err != nil {
		t.Fatalf("History after migration: %v", err)
	}
	if msgs[0].File == nil || msgs[0].File.FileID != "f1" {
		t.Errorf("file ref after migration = %+v, want f1", msgs[0].File)
	}
}
