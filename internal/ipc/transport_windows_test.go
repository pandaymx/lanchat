//go:build windows

package ipc

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

// pipeSeq 保证并行测试使用的命名管道名唯一。
var pipeSeq int64

// testAddr 返回唯一的命名管道名。
func testAddr(t *testing.T) string {
	t.Helper()
	n := atomic.AddInt64(&pipeSeq, 1)
	return `\\.\pipe\lanchat-test-` + strconv.FormatInt(n, 10)
}

// dialTest 以短超时拨号命名管道。
func dialTest(addr string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(addr, &timeout)
}
