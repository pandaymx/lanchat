//go:build windows

package ipc

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

// pipeConfig 命名管道安全配置：仅本机访问。
var pipeConfig = &winio.PipeConfig{}

// listen 在给定命名管道名上监听，path 形如 `\\.\pipe\lanchat`。
func listen(path string) (net.Listener, error) {
	return winio.ListenPipe(path, pipeConfig)
}

// ListenAndServe 在 path 命名管道上监听并阻塞处理连接，直到 Close 被调用。
func (s *Server) ListenAndServe(path string) error {
	l, err := listen(path)
	if err != nil {
		return err
	}
	return s.serve(l)
}
