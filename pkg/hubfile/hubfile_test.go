package hubfile_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubfile"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakeMetaStore 是 MetaStore 的内存实现（单测不依赖 SQL）。
type fakeMetaStore struct {
	m map[string]protocol.FileMeta
}

func newFakeMeta() *fakeMetaStore { return &fakeMetaStore{m: make(map[string]protocol.FileMeta)} }

func (f *fakeMetaStore) SaveFileMeta(_ context.Context, m protocol.FileMeta) error {
	f.m[m.FileID] = m
	return nil
}

func (f *fakeMetaStore) GetFileMeta(_ context.Context, id string) (protocol.FileMeta, error) {
	m, ok := f.m[id]
	if !ok {
		return protocol.FileMeta{}, core.ErrNotFound
	}
	return m, nil
}

func newTestService(t *testing.T, maxSize int64) *hubfile.Service {
	t.Helper()
	svc, err := hubfile.New(filepath.Join(t.TempDir(), "files"), newFakeMeta(), maxSize)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestService_SaveOpen_RoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, 0)

	ref, err := svc.Save(ctx, strings.NewReader("hello world"), "doc.txt", "text/plain")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ref.FileID == "" || len(ref.FileID) != 32 {
		t.Errorf("FileID = %q, want 32 hex", ref.FileID)
	}
	if ref.Name != "doc.txt" || ref.Size != 11 || ref.Mime != "text/plain" {
		t.Errorf("ref = %+v", ref)
	}

	meta, rc, err := svc.Open(ctx, ref.FileID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello world" {
		t.Errorf("content = %q, want hello world", got)
	}
	if meta.Name != "doc.txt" || meta.Size != 11 || meta.CreatedAt == 0 {
		t.Errorf("meta = %+v", meta)
	}
}

func TestService_Save_SanitizesName(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, 0)

	// 路径穿越尝试：保存的展示名必须只剩 basename，且不产生目录外文件。
	ref, err := svc.Save(ctx, strings.NewReader("x"), "../../evil.sh", "")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ref.Name != "evil.sh" {
		t.Errorf("name = %q, want evil.sh", ref.Name)
	}
	if ref.Mime != "application/octet-stream" {
		t.Errorf("mime = %q, want octet-stream fallback", ref.Mime)
	}
	// 逃逸检查：展示名带路径穿越时，files 目录之外不得出现 evil.sh
	// （files 目录本身是 TempDir 的子目录，必然存在；要断的是目录外
	// 没有按原名落盘的文件）。
	if _, err := os.Stat(filepath.Join(filepath.Dir(svc.Dir()), "evil.sh")); err == nil {
		t.Error("evil.sh escaped the files dir")
	}
}

func TestService_Save_TooLarge(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, 8) // 上限 8 字节

	ref, err := svc.Save(ctx, strings.NewReader("0123456789"), "big.bin", "")
	if err == nil {
		t.Fatalf("Save(10 bytes, limit 8) = %+v, want error", ref)
	}
	if !errors.Is(err, hubfile.ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	// 超限不留文件：dir 里应当为空。
	entries, _ := os.ReadDir(svc.Dir())
	if len(entries) != 0 {
		t.Errorf("dir not empty after failed save: %d entries", len(entries))
	}

	// 恰好等于上限：允许。
	if _, err := svc.Save(ctx, strings.NewReader("01234567"), "ok.bin", ""); err != nil {
		t.Errorf("Save(exact limit) = %v, want nil", err)
	}
}

func TestService_Open_InvalidID_NotFound(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, 0)

	for _, id := range []string{"", "../etc/passwd", "abc", strings.Repeat("0", 31), "ZZZZ" + strings.Repeat("0", 28)} {
		if _, _, err := svc.Open(ctx, id); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("Open(%q) = %v, want ErrNotFound", id, err)
		}
	}
	// 合法形态但未注册：NotFound
	if _, _, err := svc.Open(ctx, strings.Repeat("a", 32)); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Open(unregistered) = %v, want ErrNotFound", err)
	}
}

func TestService_Save_PersistsAcrossReopen(t *testing.T) {
	// 真实场景：hub 重启后元信息来自 store（这里 fake），blob 仍在磁盘。
	dir := filepath.Join(t.TempDir(), "files")
	store := newFakeMeta()
	ctx := context.Background()

	svc1, err := hubfile.New(dir, store, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref, err := svc1.Save(ctx, bytes.NewReader([]byte{1, 2, 3}), "b.bin", "application/octet-stream")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 重开服务（同一 dir + 同一 store）：仍能取回。
	svc2, err := hubfile.New(dir, store, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	meta, rc, err := svc2.Open(ctx, ref.FileID)
	if err != nil {
		t.Fatalf("Open after reopen: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, []byte{1, 2, 3}) || meta.Size != 3 {
		t.Errorf("after reopen: content=%v meta=%+v", got, meta)
	}
}
