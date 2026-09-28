package transfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadMetaRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.meta")
	want := &metaState{
		TransferID: "tx-1",
		Name:       "a.bin",
		Size:       100,
		SHA256:     "abc",
		ChunkSize:  ChunkSize,
		BytesDone:  40,
	}
	if err := saveMeta(path, want); err != nil {
		t.Fatalf("saveMeta: %v", err)
	}
	got, err := loadMeta(path)
	if err != nil {
		t.Fatalf("loadMeta: %v", err)
	}
	if got.TransferID != want.TransferID || got.BytesDone != want.BytesDone || got.UpdatedAt == "" {
		t.Fatalf("meta round trip mismatch: %+v", got)
	}
}

func TestLoadMetaMissing(t *testing.T) {
	got, err := loadMeta(filepath.Join(t.TempDir(), "nope.meta"))
	if err != nil {
		t.Fatalf("缺失 meta 应返回 nil 错误，got %v", err)
	}
	if got != nil {
		t.Fatalf("缺失 meta 应返回 nil，got %+v", got)
	}
}

func TestRecoverOffset(t *testing.T) {
	const size = int64(1000)

	t.Run("无meta时part被清除返回0", func(t *testing.T) {
		dir := t.TempDir()
		part := filepath.Join(dir, "f.part")
		if err := os.WriteFile(part, make([]byte, 50), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := &ReceiveConfig{TransferID: "tx", Size: size, SHA256: "s"}
		off, err := recoverOffset(filepath.Join(dir, "f.meta"), part, cfg)
		if err != nil || off != 0 {
			t.Fatalf("off=%d err=%v", off, err)
		}
		if _, err := os.Stat(part); !os.IsNotExist(err) {
			t.Fatal("无 meta 的残留 .part 应被删除")
		}
	})

	t.Run("part小于meta时收敛到part", func(t *testing.T) {
		dir := t.TempDir()
		part := filepath.Join(dir, "f.part")
		metaPath := filepath.Join(dir, "f.meta")
		if err := os.WriteFile(part, make([]byte, 30), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := saveMeta(metaPath, &metaState{
			TransferID: "tx", Size: size, SHA256: "s", BytesDone: 80,
		}); err != nil {
			t.Fatal(err)
		}
		cfg := &ReceiveConfig{TransferID: "tx", Size: size, SHA256: "s"}
		off, err := recoverOffset(metaPath, part, cfg)
		if err != nil || off != 30 {
			t.Fatalf("off=%d err=%v, want 30", off, err)
		}
	})

	t.Run("身份不一致从头开始", func(t *testing.T) {
		dir := t.TempDir()
		part := filepath.Join(dir, "f.part")
		metaPath := filepath.Join(dir, "f.meta")
		if err := os.WriteFile(part, make([]byte, 30), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := saveMeta(metaPath, &metaState{
			TransferID: "other", Size: size, SHA256: "s", BytesDone: 30,
		}); err != nil {
			t.Fatal(err)
		}
		cfg := &ReceiveConfig{TransferID: "tx", Size: size, SHA256: "s"}
		off, err := recoverOffset(metaPath, part, cfg)
		if err != nil || off != 0 {
			t.Fatalf("off=%d err=%v, want 0", off, err)
		}
	})

	t.Run("正常恢复取meta值", func(t *testing.T) {
		dir := t.TempDir()
		part := filepath.Join(dir, "f.part")
		metaPath := filepath.Join(dir, "f.meta")
		if err := os.WriteFile(part, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := saveMeta(metaPath, &metaState{
			TransferID: "tx", Size: size, SHA256: "s", BytesDone: 60,
		}); err != nil {
			t.Fatal(err)
		}
		cfg := &ReceiveConfig{TransferID: "tx", Size: size, SHA256: "s"}
		off, err := recoverOffset(metaPath, part, cfg)
		if err != nil || off != 60 {
			t.Fatalf("off=%d err=%v, want 60", off, err)
		}
	})
}

func TestMetaWriterThrottle(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "f.meta")
	state := &metaState{TransferID: "tx", BytesDone: 0}
	mw := newMetaWriter(metaPath, state, MinCreditBytes, time.Hour)

	// 小步进、未到 step：不落盘。
	if err := mw.update(10, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("未到阈值不应落盘")
	}
	// force：立即落盘。
	if err := mw.update(10, true); err != nil {
		t.Fatal(err)
	}
	got, err := loadMeta(metaPath)
	if err != nil || got.BytesDone != 10 {
		t.Fatalf("flush 后 bytesDone=%d err=%v", got.BytesDone, err)
	}
}
