package fake

import (
	"context"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// revConn 把客户端视角的 conn 反转成 Hub 视角的 hubstate.Peer。
//
// 背景：fake 的 conn 是成对使用的——客户端拿着它 Send（写 outbound），
// Hub 得从 outbound 读；Hub 想发给客户端得写 inbound，客户端从 Recv（inbound）读。
// 也就是说同一个对象，两端的 Send/Recv 方向是反的。
//
// 而 hubstate.Router 只认 Peer 一种视角（Hub 视角：Send=发给客户端，Recv=收客户端的）。
// 真 WebSocket 场景下 accept 出的 conn 天然就是 Hub 视角，不需要适配；
// fake 因为是"一根管道两头用"，需要这层反转。
//
// 这是唯一一处 fake 与真 transport 的结构差异，且被封在 40 行内——
// Router 那几百行逻辑两边共用。
type revConn struct {
	c *conn
}

// newRevConn 包装一条 conn 为 Hub 侧 Peer。
func newRevConn(c *conn) *revConn { return &revConn{c: c} }

// Recv 读客户端发来的帧 —— 即客户端 Send 进 outbound 的那些。
func (r *revConn) Recv(ctx context.Context) (protocol.Frame, error) {
	return r.c.readOutbound(ctx)
}

// Send 往客户端写帧 —— 落到客户端 Recv 会读的 inbound。
func (r *revConn) Send(ctx context.Context, f protocol.Frame) error {
	return r.c.pushInbound(ctx, f)
}

// Close 关闭整条管道，两端都会感知到。
func (r *revConn) Close() error { return r.c.Close() }

// Closed 转发连接关闭信号。
func (r *revConn) Closed() <-chan struct{} { return r.c.Closed() }

// DeviceID 返回该连接登记的设备 ID。
// Hub 侧用它做「这条连接属于谁」的初步判断，权威值以 FKHello 为准。
func (r *revConn) DeviceID() string { return r.c.LocalDeviceID() }
