package fake

import (
	"context"
	"io"
	"sync"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// conn 是 Transport 与 Hub 之间的连接单元。
//
// 它通过两个 buffered channel（inbound, outbound）模拟双向字节流：
//   - Send 把帧写进 outbound —— Hub 端从这读
//   - Recv 从 inbound 读 —— Hub 端往这写
//
// 关闭语义：
//   - Close 把 closedCh 关闭，让 Recv/Send 立刻返回 ErrClosed；
//   - 不会主动 drain 队列，避免调用方拿到过期帧当成"最新事件"。
type conn struct {
	deviceID string

	inbound   chan protocol.Frame
	outbound  chan protocol.Frame
	closeOnce sync.Once
	closedCh  chan struct{}
}

// newConn 创建一个通道容量合适的连接。
func newConn(deviceID string) *conn {
	return &conn{
		deviceID: deviceID,
		// buffer 控制在 64：业务测试一般单 conn 不会瞬时堆积比这多的消息；
		// 真阻塞由 Hub.broadcast 在压力测试下自然出现，配合 t.Cleanup 兜底。
		inbound:  make(chan protocol.Frame, 64),
		outbound: make(chan protocol.Frame, 64),
		closedCh: make(chan struct{}),
	}
}

// Send 写一帧到 outbound。ctx 取消 / 已关闭 都返回错误。
func (c *conn) Send(ctx context.Context, f protocol.Frame) error {
	select {
	case <-c.closedCh:
		return core.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case c.outbound <- f:
		return nil
	}
}

// Recv 从 inbound 读一帧。EOF 与 ErrClosed 都不重试。
func (c *conn) Recv(ctx context.Context) (protocol.Frame, error) {
	select {
	case <-c.closedCh:
		return protocol.Frame{}, io.EOF
	case <-ctx.Done():
		return protocol.Frame{}, ctx.Err()
	case f, ok := <-c.inbound:
		if !ok {
			return protocol.Frame{}, io.EOF
		}
		return f, nil
	}
}

// Close 幂等关闭。多次调用安全。
func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closedCh)
	})
	return nil
}

// Closed 返回一个监听连接关闭的 channel。
func (c *conn) Closed() <-chan struct{} { return c.closedCh }

// LocalDeviceID 返回 FakeTransport 注册时分配的 ID。
// 实际业务实现里这是 server 给的 conn id；这里直接复用 deviceID。
func (c *conn) LocalDeviceID() string { return c.deviceID }

// pushInbound 是 Hub 用来往客户端方向投递帧的内部方法。
// 不暴露在 core.Conn 接口里——只是 Hub 的 wire 服务路径要走它。
func (c *conn) pushInbound(ctx context.Context, f protocol.Frame) error {
	select {
	case <-c.closedCh:
		return core.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case c.inbound <- f:
		return nil
	}
}

// readOutbound 是 Hub 用来从客户端拉帧的内部方法。
func (c *conn) readOutbound(ctx context.Context) (protocol.Frame, error) {
	select {
	case <-c.closedCh:
		return protocol.Frame{}, io.EOF
	case <-ctx.Done():
		return protocol.Frame{}, ctx.Err()
	case f, ok := <-c.outbound:
		if !ok {
			return protocol.Frame{}, io.EOF
		}
		return f, nil
	}
}
