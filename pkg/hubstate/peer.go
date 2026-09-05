// Package hubstate 提供 Hub 的「与传输无关」的核心状态机。
//
// 它是 fake Hub（M1 测试用）与真 Hub（M2 WebSocket 服务端）的共享内核。
// 之所以要抽出来，是为了让二者跑出**完全一致的协议语义**——
// 否则 M5 的「抽象压力测试」（换 Transport 而 pkg/core 零修改）无从谈起。
//
// 本包只有状态与路由，没有任何网络细节：
//   - 它不 import net/http、不 import websocket
//   - 它只通过 Peer 接口与外界通信
//   - Transport 实现负责「怎么把字节变成 Frame」，hubstate 负责「Frame 来了怎么办」
//
// 子职责拆分：
//   - peer.go     —— Hub 侧看待连接的视角（与 core.Conn 的差异见该文件注释）
//   - seq.go      —— ServerSeq 单调分配器
//   - registry.go —— 在线连接表（User → 多 Device → 多 Conn，见 ADR-008）
//   - history.go  —— 内存历史冗余备份（补发用）
//   - router.go   —— 帧路由：把每种 FrameKind 映射到状态变更 + 投递
package hubstate

import (
	"context"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// Peer 是 Hub 侧看待一条连接的视角。
//
// 它与 core.Conn 的关键区别在**语义方向**：
//
//	core.Conn 是「持有者视角」—— 谁拿着 Conn，Send 就是它往外发，Recv 就是它收别人的。
//	Hub 侧 accept 出的连接，持有者就是 Hub，所以 core.Conn 的 Send/Recv 方向天然正确。
//
// 但 fake 的 conn 是从**客户端视角**实现的（Send 写 outbound 给 Hub 读，
// Recv 从 inbound 读 Hub 塞进来的），Hub 得反过来用它的私有方法。
//
// 为了让 hubstate 不被这个差异污染，引入 Peer 作为统一抽象：
//
//	ws   Transport  —— accept 出的 conn 直接实现 Peer（Send/Recv 方向天然正确）
//	fake Transport  —— 用反转适配器把 conn 包成 Peer（见 pkg/transport/fake/rev.go）
//
// 这样 router.go 里写一次逻辑，两种 transport 共用。
type Peer interface {
	// Recv 读对端（客户端）发来的下一帧。
	// 连接关闭时返回 io.EOF；ctx 取消时返回 ctx.Err()。
	Recv(ctx context.Context) (protocol.Frame, error)

	// Send 往对端（客户端）写一帧。
	// 连接已关闭时返回 core.ErrClosed。
	Send(ctx context.Context, f protocol.Frame) error

	// Close 关闭连接。必须幂等：重复调用返回 nil。
	Close() error

	// Closed 返回一个 channel，连接（本地或远端）关闭后该 channel 被关闭。
	// 用于让 Hub 在连接断开时清理注册表，不依赖 Recv 返回错误这一条路径。
	Closed() <-chan struct{}

	// DeviceID 返回该连接绑定的设备 ID。
	//
	// 握手前可能为空（或为 transport 层预分配的临时 ID）；
	// FKHello 处理成功后 Router 会把注册表里的条目改写到协议声明的 DeviceID。
	DeviceID() string
}
