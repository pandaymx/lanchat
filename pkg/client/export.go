// Package client 的 v1.2 备份导出：Export 从 hub 的 GET /api/export
// 拉全量 JSON 备份落盘，依赖 SetFileBase 已配置（与文件传输同一 HTTP 数据面）。
package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/pandaymx/lanchat/pkg/core"
)

// Export 把 hub 的全量备份（GET /api/export）下载到 path。
//
// 依赖 SetFileBase 已配置（与文件传输同一 HTTP 数据面）；未配置返回
// ErrFileUnconfigured。写入采用流式落盘：备份可能很大，不整读进内存。
func (c *Client) Export(ctx context.Context, path string) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	if c.fileBase == "" {
		return ErrFileUnconfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileBase+"/api/export", nil)
	if err != nil {
		return fmt.Errorf("export: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("export: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("export: hub returned %s", resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("export: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("export: write %s: %w", path, err)
	}
	return nil
}
