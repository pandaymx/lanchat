// Package mobile 是 LANChat 移动端（Android AAR / iOS XCFramework）
// 与 Go 核心之间的 gomobile 绑定门面包。
//
// 设计约束（见 AGENTS.md §7 移动端诚实边界）：
//   - gomobile bind 的生成代码位于模块外，无法 import internal/*，
//     因此本包导出签名中不得出现 internal/core、internal/appapi 的类型；
//   - 不使用 chan / 泛型 / 复杂嵌套，事件回调用 Listener interface；
//   - 本包只做薄适配：持有 *core.Client，在 appapi ↔ mobile DTO 之间值拷贝，
//     业务逻辑全部下沉 core。
//
// 字段与 api/ipc.schema.json 一一对齐；枚举以字符串常量导出
// （gomobile 会把 Go 的 typed string 退化为宿主语言 String）。
package mobile

// 连接状态（对应 schema ConnState）。
const (
	ConnDisconnected = "disconnected"
	ConnConnecting   = "connecting"
	ConnConnected    = "connected"
	ConnAuthFailed   = "auth_failed"
)

// 文件传输状态（对应 schema TransferState）。
const (
	TransferPending  = "pending"
	TransferActive   = "active"
	TransferPaused   = "paused"
	TransferDone     = "done"
	TransferFailed   = "failed"
	TransferCanceled = "canceled"
)

// 传输方向（对应 schema TransferDirection）。
const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"
)

// 传输路径类型（对应 schema PathKind）。
const (
	KindUnicast = "unicast"
	KindSwarm   = "swarm"
	KindChannel = "channel"
)

// Peer 在线用户（在线表条目）。
type Peer struct {
	ID       string
	Nickname string
	OS       string
	Status   string
}

// Server mDNS 浏览到的中心节点候选。
type Server struct {
	Name     string
	ID       string
	Addr     string
	Version  string
	AuthMode string
}

// Transfer 一个文件传输任务的快照。
type Transfer struct {
	ID          string
	Direction   string
	State       string
	Kind        string
	PeerID      string
	GroupID     string
	Name        string
	Size        int64
	BytesDone   int64
	SpeedBps    int64
	ViaRelay    bool
	ErrorReason string
}

// Channel G2 自定义频道。
type Channel struct {
	ID      string
	Name    string
	OwnerID string
	Private bool
	Topic   string
	Members []string
}

// State GetState 返回的全量快照。
type State struct {
	Conn      string
	Server    string
	SelfID    string
	Nickname  string
	Peers     []Peer
	Transfers []Transfer
	Channels  []Channel
}
