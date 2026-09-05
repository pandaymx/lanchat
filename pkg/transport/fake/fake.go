// Package fake 提供 core.Transport 的同进程内存实现。
//
// 主要用途：
//  1. 单元测试：FakeTransport 与 MemoryStore 一起跑出"发→收→存→补发"场景，无需任何端口
//  2. 调试：在 IDE 里直接 step 单进程代码，不依赖网络栈
//
// 关键设计：
//   - 两个 conn 之间通过 *Hub 路由（Hub 是 fake 包内的"零号 hub"），不直接 1-1 wire
//   - Hub 的**全部协议语义委托给 hubstate.Router**，与真 WebSocket Hub 共用同一份逻辑
//   - fake 与真 transport 的唯一结构差异是 revConn（见 rev.go）：
//     真 transport accept 出的连接天然是 Hub 视角，fake 的管道两头共用需要反转
//
// 这样做的收益是 M5 的抽象压力测试有据可依：
// 换 transport 时 pkg/core 与 hubstate 都零修改，只换 Dial/Listen 的构造。
package fake

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubstate"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// addrPrefix 是 FakeTransport 的地址 scheme，区分于真网络的 ws://、tcp:// 等。
const addrPrefix = "memory://"

// Transport 是 core.Transport 的内存实现。
type Transport struct {
	mu   sync.Mutex
	hubs map[string]*Hub // key = address (去除 scheme)
}

// New 创建新的内存 Transport。
func New() *Transport {
	return &Transport{hubs: make(map[string]*Hub)}
}

// NewHub 在 addr 注册一个新的 FakeHub。
//
// 调用方负责在结束时调用 hub.Close() 来释放资源。
// 注册后即可被 Dial 找到。
func (t *Transport) NewHub(addr string) *Hub {
	if !strings.HasPrefix(addr, addrPrefix) {
		addr = addrPrefix + addr
	}
	trimmed := strings.TrimPrefix(addr, addrPrefix)
	t.mu.Lock()
	defer t.mu.Unlock()
	h := newHub(trimmed)
	t.hubs[trimmed] = h
	return h
}

// ErrNoHub 在 Dial 时找不到对应地址时返回。
var ErrNoHub = errors.New("fake: no hub at that address")

// Dial 模拟客户端拨号到 target 地址。仅按完整地址精确匹配。
//
// ctx 用于取消连接建立过程；ctx 取消后 Hub 侧的读循环退出并注销连接，
// 这是给 Transport 用户的标准约定。
func (t *Transport) Dial(ctx context.Context, target string, hello protocol.Hello) (core.Conn, error) {
	t.mu.Lock()
	addr := strings.TrimPrefix(target, addrPrefix)
	h, ok := t.hubs[addr]
	t.mu.Unlock()
	if !ok {
		return nil, ErrNoHub
	}
	return h.dial(ctx, hello), nil
}

// Listen 是 stub：返回 ctx.Err() 之前一直阻塞，等待 Close。
//
// Hub 由 NewHub 注册并由调用方驱动 Accept，本方法不真正启动 listener。
func (t *Transport) Listen(ctx context.Context, _ string, _ func(core.Conn, protocol.Hello) error) error {
	<-ctx.Done()
	return ctx.Err()
}

// Close 释放 Transport 注册的所有 Hub。
func (t *Transport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, h := range t.hubs {
		h.Close()
	}
}

// Hub 模拟服务端：accept Dial 过来的 Conn，交给 hubstate.Router 处理全部帧。
//
// Hub 本身不再实现任何协议逻辑——那些都在 hubstate.Router 里，
// 与真 WebSocket Hub 是同一份代码。这里只负责：
//   - 把客户端视角的 conn 反转成 Hub 视角的 Peer（revConn）
//   - 把连接交给 Router 接管
//
// Hub 不感知业务规则（发言权限、是否群成员等）—— 那些放到真 Hub 的业务层，
// 且即便真 Hub 加了权限校验，Router 这一层也保持不变。
type Hub struct {
	addr string

	mu     sync.Mutex
	store  core.Store
	router *hubstate.Router // 懒构造：见 AttachStore 的说明
	closed bool

	// nextConn 用于给 conn 生成不重复的临时标识。
	// 它不是 ServerSeq（那个由 Router 的 Sequencer 分配），
	// 只是为了让同设备的多条连接可区分，便于调试。
	nextConn int64
}

func newHub(addr string) *Hub {
	return &Hub{addr: addr}
}

// AttachStore 绑定落库后端。必须在第一次 Dial/Accept 之前调用。
//
// 之所以能后绑定：Router 是懒构造的，第一次有连接进来时才按当时的 store 建。
// 这个约定让测试能先建 Hub 再注 store，而不必改 Dial 的签名。
func (h *Hub) AttachStore(s core.Store) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store = s
	// 若 Router 已建但还没有连接，重建以应用新 store；
	// 已经接过连接就不再动，避免半途换库导致行为不一致。
	if h.router != nil && h.router.Registry().Count() == 0 {
		h.router = hubstate.NewRouter(&hubstate.RouterConfig{Store: s})
	}
}

// routerLocked 取（必要时构造）Router。调用方须持有 h.mu。
func (h *Hub) routerLocked() *hubstate.Router {
	if h.router == nil {
		h.router = hubstate.NewRouter(&hubstate.RouterConfig{Store: h.store})
	}
	return h.router
}

// Accept 把一个外部构造的 Conn 注入 Hub，等价于服务端接受了一个连接。
//
// 传入的 c 必须是 NewConn() 造出来的（客户端视角），
// Hub 会用 revConn 反转成 Hub 视角后交给 Router。
func (h *Hub) Accept(ctx context.Context, c core.Conn, hello protocol.Hello) error {
	fc, ok := c.(*conn)
	if !ok {
		return errors.New("fake: Hub.Accept 只能接受 fake.NewConn() 构造的 Conn")
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return core.ErrClosed
	}
	r := h.routerLocked()
	h.mu.Unlock()

	// 预先握手：Accept 的调用方已经把 hello 给全了，
	// 不需要再等客户端发 FKHello（真 transport 路径下那一步是必须的）。
	rev := newRevConn(fc)
	peerID := r.Registry().Add(rev, hello.DeviceID, hello.UserID)
	r.Registry().MarkHello(peerID, hello.DeviceID, hello.UserID)

	r.Attach(ctx, rev)
	return nil
}

// NewConn 导出构造函数，供测试手工构造 Conn 后 Accept 进 Hub。
func NewConn(deviceID string) core.Conn { return newConn(deviceID) }

// dial 是 Transport.Dial 的内部辅助：从客户端视角 new 一个 conn，
// 反转后交给 Router 接管，把客户端视角的 conn 返回给调用方。
//
// ctx 用于取消 Router 的读循环：ctx 取消时该连接被注销。
func (h *Hub) dial(ctx context.Context, hello protocol.Hello) core.Conn {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return &conn{deviceID: hello.DeviceID, closedCh: closedChan()}
	}
	h.nextConn++
	c := newConn(hello.DeviceID + "-" + itoa(h.nextConn))
	r := h.routerLocked()
	h.mu.Unlock()

	// Attach 同步登记，返回后 ConnCount 立即可见（避免调用方断言时的竞态）
	r.Attach(ctx, newRevConn(c))
	return c
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// itoa 把 int64 转 string，避免 fmt 在 hot path 引入开销。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ConnCount 返回当前在线 conn 数量，用于测试断言。
func (h *Hub) ConnCount() int {
	h.mu.Lock()
	r := h.routerLocked()
	h.mu.Unlock()
	return r.Registry().Count()
}

// Close 断开所有 Conn 并标记 Hub 已关闭。
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	r := h.router
	h.router = nil
	h.mu.Unlock()

	if r != nil {
		r.Close()
	}
}
