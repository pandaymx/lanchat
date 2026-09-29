//go:build !windows

package ipc

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

// testAddr 返回临时 Unix socket 路径。
func testAddr(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lanchat.sock")
}

// dialTest 以短超时拨号 Unix socket。
func dialTest(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}
