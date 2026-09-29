//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
)

// listen 在给定 Unix socket 路径上监听。
// 若路径已残留（上次进程未清理），先删除再绑定，避免 "address already in use"。
func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// 残留 socket 不是普通文件，Remove 不影响真实数据。
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return l, nil
}

// ListenAndServe 在 path 上监听并阻塞处理连接，直到 Close 被调用。
func (s *Server) ListenAndServe(path string) error {
	l, err := listen(path)
	if err != nil {
		return err
	}
	return s.serve(l)
}
