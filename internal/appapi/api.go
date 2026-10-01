// Package appapi 是 LANChat 五端 UI 与 Go 核心之间的契约层（唯一真源）。
//
// 本文件在 M0 阶段只定义方法集合、数据类型与事件回调接口，不含任何实现，
// 也不依赖 internal 下的业务包。机器可读契约见仓库根目录 api/ipc.schema.json，
// 任何端不得使用 schema 之外的方法或字段。
//
// 契约纪律：
//   - 新增字段必须可缺省解析；
//   - minor 版本内不允许改变已有字段语义；
//   - schema 变更须 A0 + 五端六方同步（见 AGENTS.md §3）。
package appapi

// ConnState 描述 daemon 与中心节点之间的连接状态。
type ConnState string

const (
	ConnDisconnected ConnState = "disconnected"
	ConnConnecting   ConnState = "connecting"
	ConnConnected    ConnState = "connected"
	ConnAuthFailed   ConnState = "auth_failed"
)

// TransferState 描述一个文件传输任务的状态。
type TransferState string

const (
	TransferPending  TransferState = "pending"
	TransferActive   TransferState = "active"
	TransferPaused   TransferState = "paused"
	TransferDone     TransferState = "done"
	TransferFailed   TransferState = "failed"
	TransferCanceled TransferState = "canceled"
)

// TransferDirection 传输方向（相对本机）。
type TransferDirection string

const (
	TransferInbound  TransferDirection = "inbound"
	TransferOutbound TransferDirection = "outbound"
)

// PathKind 传输路径类型：单播 / G1 全体 / G2 自定义频道。
type PathKind string

const (
	PathUnicast PathKind = "unicast"
	PathSwarm   PathKind = "swarm"
	PathChannel PathKind = "channel"
)

// Peer 在线用户（在线表条目）。
type Peer struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	OS       string `json:"os"`
	Status   string `json:"status"`
}

// Server mDNS 浏览到的中心节点候选。
type Server struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Addr     string `json:"addr"`
	Version  string `json:"version"`
	AuthMode string `json:"authMode"`
}

// Transfer 一个文件传输任务的快照。
type Transfer struct {
	ID          string            `json:"id"`
	Direction   TransferDirection `json:"direction"`
	State       TransferState     `json:"state"`
	Kind        PathKind          `json:"kind"`
	PeerID      string            `json:"peerId,omitempty"`
	GroupID     string            `json:"groupId,omitempty"`
	Name        string            `json:"name"`
	Size        int64             `json:"size"`
	BytesDone   int64             `json:"bytesDone"`
	SpeedBps    int64             `json:"speedBps"`
	ViaRelay    bool              `json:"viaRelay"`
	ErrorReason string            `json:"errorReason,omitempty"`
}

// Channel G2 自定义频道。
type Channel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	OwnerID string   `json:"ownerId"`
	Private bool     `json:"private,omitempty"`
	Topic   string   `json:"topic,omitempty"`
	Members []string `json:"members"`
}

// State GetState 返回的全量快照。
type State struct {
	Conn      ConnState  `json:"conn"`
	Server    string     `json:"server,omitempty"`
	SelfID    string     `json:"selfId,omitempty"`
	Nickname  string     `json:"nickname"`
	Peers     []Peer     `json:"peers"`
	Transfers []Transfer `json:"transfers"`
	Channels  []Channel  `json:"channels"`
}

// API 是 daemon 暴露给各端 UI 的全部方法（对齐方案 §12.1）。
// JSON-RPC 侧每个方法对应一个 request；gomobile 侧直接实现本接口。
type API interface {
	// GetState 返回连接态 + 自己 + 在线表 + 传输快照。
	GetState() State

	// Connect 手动连接指定中心节点。
	Connect(addr, psk string) error
	// BrowseServers 触发 mDNS 重扫。
	BrowseServers() []Server

	// SendText 发送文本；group 为空表示单播，"*" 为 G1 全体，其余为 G2 频道。
	SendText(to, text, group string) (msgID string, err error)
	// SendSticker 发送本地表情（path 为已缓存的贴纸文件路径）。
	SendSticker(to, path string) (msgID string, err error)

	// OfferFile 发起 1:1 文件发送。
	OfferFile(to, path string) (transferID string, err error)
	// OfferFileToGroup 发起 1:N 发送（G1 全体或 G2 频道，需 UI 二次确认）。
	OfferFileToGroup(group, path string) (transferID string, err error)
	// RespondFile 应答收到的文件邀请；accept=true 时 dest 为保存路径。
	RespondFile(transferID string, accept bool, dest string) error
	// PauseFile 暂停传输。
	PauseFile(transferID string) error
	// ResumeFile 恢复传输。
	ResumeFile(transferID string) error
	// CancelFile 取消传输。
	CancelFile(transferID string) error

	// SetNickname 修改本机昵称。
	SetNickname(name string) error
	// PickDownloadDir 设置默认下载目录（UI 用原生对话框选择后回调 core）。
	PickDownloadDir(path string) error

	// ChannelCreate 创建 G2 自定义频道；private 频道仅可经 ChannelInvite 加入。
	ChannelCreate(name, topic string, private bool) (channelID string, err error)
	// ChannelJoin 加入 G2 自定义频道（private 频道会被拒绝）。
	ChannelJoin(channelID string) error
	// ChannelInvite 邀请在线成员加入频道（仅 owner）。
	ChannelInvite(channelID, memberID string) error
	// ChannelLeave 退出 G2 自定义频道。
	ChannelLeave(channelID string) error
	// ChannelList 列出当前可见频道。
	ChannelList() []Channel
}

// Listener 是 core → UI 的事件回调集合（notification）。
// 桌面端经 JSON-RPC notifications 下发；移动端由 gomobile 注册 Listener 实现。
type Listener interface {
	// OnConnChanged 连接状态变化。
	OnConnChanged(state ConnState, reason string)
	// OnPeerJoined 新用户上线。
	OnPeerJoined(peer Peer)
	// OnPeerLeft 用户下线。
	OnPeerLeft(peerID string)

	// OnMessageReceived 收到文本 / 表情。
	OnMessageReceived(from, group, msgID, typ, text string)

	// OnTransferProgress 传输进度更新。
	OnTransferProgress(t Transfer)
	// OnTransferDone 传输完成（含校验通过）。
	OnTransferDone(transferID string)
	// OnTransferFailed 传输失败。
	OnTransferFailed(transferID, reason string)

	// OnGroupMatrix 群组任务的块可用性矩阵更新（G1/G2）。
	OnGroupMatrix(groupID, transferID string, haveBitmap []byte)
	// OnChannelUpdated 频道列表 / 成员变化。
	OnChannelUpdated(channels []Channel)
}
