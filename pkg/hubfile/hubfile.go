// Package hubfile 提供 hub 侧文件 blob 的持久化与按 ID 取回（M9）。
//
// 职责边界：
//   - 本包管「文件二进制怎么落盘、怎么取回」；
//   - 元信息（FileMeta）委托给 store 持久化（重启后仍可按 ID 查询）；
//   - HTTP 面（multipart 解析 / Range 响应）在 internal/hubapi。
//
// 安全模型：FileID 由本服务用 crypto/rand 生成（16 字节 → 32 hex），
// 不接受调用方指定；存储路径永远是 dir/<FileID> 单层布局，Open 时再
// 校验一次 ID 形态（非 32 hex 一律 NotFound），目录遍历无从发生。
package hubfile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// ErrTooLarge 表示文件超过大小上限（Save 的 sentinel error）。
var ErrTooLarge = errors.New("hubfile: file too large")

// MetaStore 是 hubfile 依赖的元信息存储窄接口（core.Store 的子集，
// 单测可塞 fake，不必起完整 store）。
type MetaStore interface {
	SaveFileMeta(ctx context.Context, m protocol.FileMeta) error
	GetFileMeta(ctx context.Context, fileID string) (protocol.FileMeta, error)
}

var fileLog = logging.New("hubfile")

// Service 提供文件持久化服务。构造后并发安全（每次 Save/Open 独立文件）。
type Service struct {
	dir     string
	store   MetaStore
	maxSize int64
}

// New 打开（必要时创建）文件目录。maxSize<=0 表示不限制单文件大小。
func New(dir string, store MetaStore, maxSize int64) (*Service, error) {
	if dir == "" {
		return nil, errors.New("hubfile: dir required")
	}
	if store == nil {
		return nil, errors.New("hubfile: store required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("hubfile: mkdir %s: %w", dir, err)
	}
	return &Service{dir: dir, store: store, maxSize: maxSize}, nil
}

// MaxSize 返回单文件大小上限（字节）；<=0 表示不限制。
func (s *Service) MaxSize() int64 { return s.maxSize }

// Dir 返回 blob 存储目录（运维查看/清理用）。
func (s *Service) Dir() string { return s.dir }

// Save 从 src 读取文件写入存储并记录元信息，返回消息携带的 FileRef。
//
// name 仅作展示：取 filepath.Base 并截断到 255 字节（防路径穿越与超长
// 文件名）；mime 为空时回落 application/octet-stream。任何失败路径都
// 不留半成品文件（写入出错/超限/元信息失败即删除）。
func (s *Service) Save(ctx context.Context, src io.Reader, name, mime string) (protocol.FileRef, error) {
	id, err := newFileID()
	if err != nil {
		return protocol.FileRef{}, err
	}
	name = sanitizeName(name)
	if mime == "" {
		mime = "application/octet-stream"
	}

	path := filepath.Join(s.dir, id)
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return protocol.FileRef{}, fmt.Errorf("hubfile: create %s: %w", id, err)
	}
	cleanup := func() { _ = dst.Close(); _ = os.Remove(path) }

	var n int64
	if s.maxSize > 0 {
		// LimitReader(max+1)：读到第 max+1 字节就停，比 max 多 1 即判超限。
		n, err = io.Copy(dst, io.LimitReader(src, s.maxSize+1))
		if err == nil && n > s.maxSize {
			cleanup()
			return protocol.FileRef{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, s.maxSize)
		}
	} else {
		n, err = io.Copy(dst, src)
	}
	if err != nil {
		cleanup()
		return protocol.FileRef{}, fmt.Errorf("hubfile: write %s: %w", id, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(path)
		return protocol.FileRef{}, fmt.Errorf("hubfile: close %s: %w", id, err)
	}

	meta := protocol.FileMeta{
		FileID:    id,
		Name:      name,
		Size:      n,
		Mime:      mime,
		CreatedAt: time.Now().UnixMilli(),
	}
	if err := s.store.SaveFileMeta(ctx, meta); err != nil {
		_ = os.Remove(path)
		return protocol.FileRef{}, fmt.Errorf("hubfile: save meta: %w", err)
	}
	fileLog.Info("file saved", "file", id, "name", name, "size", n)
	return protocol.FileRef{FileID: id, Name: name, Size: n, Mime: mime}, nil
}

// Open 按 FileID 返回元信息与可读流（调用方负责 Close）。
//
// 未命中（库无记录或磁盘无文件）返回 core.ErrNotFound；ID 形态非法
// （非 32 hex）同样按 NotFound 处理，不泄露存储布局。
func (s *Service) Open(ctx context.Context, fileID string) (protocol.FileMeta, io.ReadCloser, error) {
	if !validID(fileID) {
		return protocol.FileMeta{}, nil, core.ErrNotFound
	}
	meta, err := s.store.GetFileMeta(ctx, fileID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return protocol.FileMeta{}, nil, core.ErrNotFound
		}
		return protocol.FileMeta{}, nil, err
	}
	f, err := os.Open(filepath.Join(s.dir, fileID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.FileMeta{}, nil, core.ErrNotFound
		}
		return protocol.FileMeta{}, nil, fmt.Errorf("hubfile: open %s: %w", fileID, err)
	}
	return meta, f, nil
}

// newFileID 生成 32 hex 字符的随机文件 ID。
func newFileID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("hubfile: rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// validID 校验 FileID 是否为 32 位小写 hex（Open 的准入闸门）。
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// sanitizeName 清洗展示用文件名：去掉目录前缀、截断超长字节。
func sanitizeName(name string) string {
	name = filepath.Base(name)
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}
