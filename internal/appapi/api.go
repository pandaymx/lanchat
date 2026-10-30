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
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	OwnerID         string   `json:"ownerId"`
	Private         bool     `json:"private,omitempty"`
	Topic           string   `json:"topic,omitempty"`
	Members         []string `json:"members"`
	Admins          []string `json:"admins,omitempty"`
	Muted           []string `json:"muted,omitempty"`
	AnnouncementAck []string `json:"announcementAck,omitempty"`
}

// ChatMessage 本地历史消息快照（M10/M11a/v1.1：随 GetState 可选返回，支持重启后恢复会话记录、结构化引用回复、@提及、表情回应表、二次编辑时间戳、置顶标记、媒体附件元信息、撤回与已读回执）。
type ChatMessage struct {
	ID         string              `json:"id"`
	From       string              `json:"from"`
	To         string              `json:"to,omitempty"`
	Group      string              `json:"group,omitempty"`
	Type       string              `json:"type"`
	Text       string              `json:"text"`
	ReplyTo    string              `json:"replyTo,omitempty"`
	ReplyQuote string              `json:"replyQuote,omitempty"`
	Mentions   []string            `json:"mentions,omitempty"`
	Reactions  map[string][]string `json:"reactions,omitempty"`
	EditedAt   int64               `json:"editedAt,omitempty"`
	Pinned     bool                `json:"pinned,omitempty"`
	MediaMime  string              `json:"mediaMime,omitempty"`
	MediaPath  string              `json:"mediaPath,omitempty"`
	Revoked    bool                `json:"revoked,omitempty"`
	ReadBy     []string            `json:"readBy,omitempty"`
	TS         int64               `json:"ts"`
}

// VoiceCallState 描述当前局域网实时语音呼叫状态（契约 v1.2）。
type VoiceCallState struct {
	State      string `json:"state"`
	PeerID     string `json:"peerId,omitempty"`
	GroupID    string `json:"groupId,omitempty"`
	LocalAddr  string `json:"localAddr,omitempty"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	FramesIn   int64  `json:"framesIn,omitempty"`
	FramesOut  int64  `json:"framesOut,omitempty"`
}

// ScreenShareState 描述当前局域网屏幕共享状态（契约 v1.2 / v6.2.0 激光笔红点坐标叠加）。
type ScreenShareState struct {
	Active      bool    `json:"active"`
	PeerID      string  `json:"peerId,omitempty"`
	GroupID     string  `json:"groupId,omitempty"`
	FPS         int     `json:"fps,omitempty"`
	LatestFrame string  `json:"latestFrame,omitempty"`
	AnnotX      float64 `json:"annotX,omitempty"`
	AnnotY      float64 `json:"annotY,omitempty"`
	AnnotNote   string  `json:"annotNote,omitempty"`
	AnnotTS     int64   `json:"annotTs,omitempty"`
}

// State GetState 返回的全量快照。
type State struct {
	Conn        ConnState         `json:"conn"`
	Server      string            `json:"server,omitempty"`
	SelfID      string            `json:"selfId,omitempty"`
	Nickname    string            `json:"nickname"`
	Peers       []Peer            `json:"peers"`
	Transfers   []Transfer        `json:"transfers"`
	Channels    []Channel         `json:"channels"`
	Messages    []ChatMessage     `json:"messages,omitempty"`
	VoiceCall   *VoiceCallState   `json:"voiceCall,omitempty"`
	ScreenShare *ScreenShareState `json:"screenShare,omitempty"`
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

	// StartVoiceCall 发起 1:1 或频道实时 UDP 语音呼叫（契约 v1.3）。
	StartVoiceCall(to, group string) (status string, err error)
	// AcceptVoiceCall 接听来电并绑定对端 UDP 语音地址（契约 v1.3）。
	AcceptVoiceCall(to, group, remoteUDP string) (status string, err error)
	// StopVoiceCall 挂断当前实时语音通话（契约 v1.3）。
	StopVoiceCall(to, group string) error

	// StartScreenShare 开启局域网低延迟 P2P 屏幕共享（契约 v1.3）。
	StartScreenShare(to, group string, fps int) (status string, err error)
	// StopScreenShare 停止屏幕共享（契约 v1.3）。
	StopScreenShare(to, group string) error
	// SendScreenAnnotation 在屏幕共享会话中发送归一化坐标激光笔批注（契约 v1.3）。
	SendScreenAnnotation(to, group string, normX, normY float64, note string) error

	// SyncFolder 扫描本地目录并执行 P2P 增量块去重同步（契约 v1.3）。
	SyncFolder(to, group, dirPath string) (synced int, err error)
	// IssueTrustCert 使用本机 Ed25519 根密钥签发组织节点准入证书（契约 v1.3）。
	IssueTrustCert(subject, org string, ttlSec int64) (token string, err error)
	// VerifyTrustCert 校验 Ed25519 组织节点准入证书（契约 v1.3）。
	VerifyTrustCert(token, rootPub string) (identity string, err error)
	// RunNetworkDiagnostics 执行局域网/Tailscale 全链路网络诊断雷达（契约 v1.3）。
	RunNetworkDiagnostics() string

	// RevokeMessage 撤回本机在时限内发送的指定消息（契约 v1.4）。
	RevokeMessage(to, group, msgID string) error
	// ReactMessage 为指定消息添加表情回应并同步对端或群组（契约 v1.4）。
	ReactMessage(to, group, msgID, emoji string) error
	// EditMessage 原地二次编辑本机已发送的消息并同步对端或群组（契约 v1.4）。
	EditMessage(to, group, msgID, newText string) error
	// PinMessage 将指定消息标记为会话置顶公告并同步对端或群组（契约 v1.4）。
	PinMessage(to, group, msgID string) error
	// MarkMessageRead 发送指定消息已读回执并清零对应会话未读计数（契约 v1.4）。
	MarkMessageRead(to, group, msgID string) error

	// SetSessionE2EE 设置或清除会话端到端对称加密密钥（契约 v1.4）。
	SetSessionE2EE(secret string) error
	// SetAutoClipboard 开启或关闭跨设备剪贴板自动监听同步模式（契约 v1.4）。
	SetAutoClipboard(enabled bool)
	// SetDND 开启或关闭全局免打扰模式（契约 v1.4）。
	SetDND(enabled bool)
	// GetHistoryPage 按会话 ID 与游标时间戳分页拉取本地加密历史消息（契约 v1.4）。
	GetHistoryPage(convID string, beforeTS int64, limit int) []ChatMessage

	// VerifyPeerFingerprint 核验本机与指定对端的 X25519 TOFU 安全码指纹（契约 v1.5）。
	VerifyPeerFingerprint(peerID string) string
	// SendBurnMessage 发送阅后即焚定时销毁消息（契约 v1.5）。
	SendBurnMessage(to, group, text string, ttlSec int) (msgID string, err error)
	// SyncHistoryWithPeer 向目标节点或频道发起离线加密历史漫游增量同步（契约 v1.5）。
	SyncHistoryWithPeer(to, group string, sinceTS int64) (synced int, err error)
	// SetBandwidthLimit 设置全局文件传输带宽限速（MB/s，<=0 表示不限速，契约 v1.5）。
	SetBandwidthLimit(limitMBps float64) string
	// CheckSilentUpdate 通过国内加速代理链检查或静默下载最新安装包（契约 v1.5）。
	CheckSilentUpdate(proxy string, download bool) (status string, err error)

	// ChannelManageMember 执行频道精细化权限治理（promote/demote/mute/unmute/kick/ack_announcement，契约 v1.6）。
	ChannelManageMember(channelID, memberID, action string) error
	// EnqueueOfflineMailbox 向中心节点加密离线投递箱投递经接收方 X25519 公钥密封的离线信封（契约 v1.6）。
	EnqueueOfflineMailbox(to, text, recipientPub string) (msgID string, err error)
	// DrainOfflineMailbox 从中心节点或本地待收投递箱拉取、解密并销毁离线加密信封（契约 v1.6）。
	DrainOfflineMailbox(fromPeer string) string

	// PairDevice 执行同账号多设备配对码生成、绑定、解绑与实时加密镜像同步（generate/bind/unbind/sync，契约 v1.7）。
	PairDevice(action, code, peerID string) (status string, err error)
	// ListPairedDevices 返回当前已绑定同账号配对设备的 JSON 列表摘要（契约 v1.7）。
	ListPairedDevices(filter string) string
	// ManageChannelWebhook 管理 G2 频道本地 HMAC-SHA256 签名 HTTP Webhook 机器人网关（create/delete/list/trigger，契约 v1.7）。
	ManageChannelWebhook(channelID, action, name, listenAddr string) (status string, err error)
	// ExportHistoryArchive 使用 PBKDF2-SHA256 + AES-256-GCM 口令将全量历史消息与 CAS 富媒体打包导出为加密归档文件（契约 v1.7）。
	ExportHistoryArchive(destPath, passphrase string) (status string, err error)
	// ImportHistoryArchive 解密并恢复加密历史归档文件中的消息与 CAS 富媒体（契约 v1.7）。
	ImportHistoryArchive(srcPath, passphrase string) (status string, err error)
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

// MessageUpdateListener 是可选的扩展事件回调接口（契约 v1.1）：
// 当消息因点赞回应、二次编辑、置顶、撤回或已读回执发生变化时，下发完整结构化 ChatMessage（对应 msg.updated）。
type MessageUpdateListener interface {
	OnMessageUpdated(msg ChatMessage)
}

// CollabListener 是可选的实时音视频协作事件回调接口（契约 v1.2）：
// 承载结构化语音呼叫状态更新（call.state）与屏幕共享帧流（screen.frame）。
type CollabListener interface {
	OnVoiceCallState(call VoiceCallState)
	OnScreenShareFrame(from, group string, fps int, frameData string)
}
