// Package integration 跑端到端测试：真 cmd/hub + 真 WS 客户端 + 真 store。
//
// 这些测试故意放在 internal/ 下，不导出：只是项目内部的"模拟用户"，
// 确保 fake Hub 与真 Hub 行为一致。每加一个新功能，相应测试要扩。
//
// 每个测试自己起一个 Hub（不同端口），互不干扰。
package integration

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubstate"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// hubFixture 起一个真 hub 在 freePort 上，返回地址与关闭函数。
//
// 不复用 cmd/hub 二进制——直接构造 Router + WS Transport 更可控，
// 避免 binary 路径差异与 LDFLAGS 注入的麻烦。
type hubFixture struct {
	addr    string
	router  *hubstate.Router
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped chan struct{}
}

// newHubFixture 起 hub 并在后台跑 Listen。失败 t.Fatal。
func newHubFixture(t *testing.T) *hubFixture {
	t.Helper()

	addr := freePort(t)
	router := hubstate.NewRouter(&hubstate.RouterConfig{
		Store: memory.New(),
	})
	tr := wstransport.New().WithPath("/ws")

	ctx, cancel := context.WithCancel(context.Background())
	fx := &hubFixture{
		addr:    addr,
		router:  router,
		cancel:  cancel,
		stopped: make(chan struct{}),
	}

	fx.wg.Add(1)
	go func() {
		defer fx.wg.Done()
		_ = tr.Listen(ctx, addr, func(conn core.Conn, _ protocol.Hello) error {
			p, ok := conn.(hubstate.Peer)
			if !ok {
				_ = conn.Close()
				return nil
			}
			router.Attach(ctx, p)
			return nil
		})
		close(fx.stopped)
	}()

	// 等端口可连（Listen 已绑端口的弱证明）。
	if err := waitForTCP(ctx, addr, 3*time.Second); err != nil {
		cancel()
		t.Fatalf("hub did not bind %s within 3s: %v", addr, err)
	}
	return fx
}

// close 优雅停 hub。
func (fx *hubFixture) close() {
	fx.cancel()
	// 等 Listen 返回（Listen 内部 Shutdown 超时是 5s）。
	select {
	case <-fx.stopped:
	case <-time.After(6 * time.Second):
		// 真到这步说明 Listen 卡住了——强行退出避免测试挂死
	}
}

// freePort 让 OS 分配一个空闲端口。返回值形如 "127.0.0.1:34567"。
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// waitForTCP 用 Dial 探测端口直到连通或超时。
func waitForTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
