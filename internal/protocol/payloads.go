package protocol

import "encoding/json"

// HelloPayload 是 HELLO 的负载（方案 §3.2）。
type HelloPayload struct {
	Nickname string     `json:"nickname"`
	DeviceID string     `json:"deviceID"`
	PSK      string     `json:"psk,omitempty"`
	Caps     ClientCaps `json:"caps"`
	OS       string     `json:"os"`
}

// ClientCaps 是客户端能力声明，服务端取交集后在 WELCOME.features 回传。
type ClientCaps struct {
	Resume bool `json:"resume"`
	Relay  bool `json:"relay"`
	TLS    bool `json:"tls"`
	Swarm  bool `json:"swarm"`
	Group  bool `json:"group"`
}

// WelcomePayload 是 WELCOME 的负载。
type WelcomePayload struct {
	SelfID            string   `json:"selfID"`
	ProtocolVersion   string   `json:"protocolVersion"`
	HeartbeatInterval int      `json:"heartbeatInterval"`
	AuthMode          string   `json:"authMode"`
	Features          []string `json:"features"`
}

// AuthFailPayload 是 AUTH_FAIL 的负载。
type AuthFailPayload struct {
	Reason    string `json:"reason"`
	Retryable bool   `json:"retryable"`
}

// ErrorPayload 是 ERROR 的负载。
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// User 是在线用户条目。
type User struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	OS       string `json:"os"`
	Status   string `json:"status,omitempty"`
}

// UserListPayload 是 USER_LIST 的负载（全量）。
type UserListPayload struct {
	Revision uint64 `json:"revision"`
	Users    []User `json:"users"`
}

// UserUpdatePayload 是 USER_JOIN / USER_LEAVE / PRESENCE_UPDATE 的负载（增量）。
type UserUpdatePayload struct {
	User     User   `json:"user"`
	Revision uint64 `json:"revision"`
}

// HeartbeatPayload 是 HEARTBEAT / HEARTBEAT_ACK 的负载。
type HeartbeatPayload struct {
	Revision   uint64 `json:"revision,omitempty"`
	ServerTime int64  `json:"serverTime,omitempty"`
}

// TextPayload 是 TEXT_MSG 的负载。
type TextPayload struct {
	Text string `json:"text"`
}

// StickerPayload 是 STICKER_MSG 的负载。
type StickerPayload struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	B64  string `json:"b64,omitempty"`
	Hash string `json:"hash"`
}

// TypingPayload 是 TYPING 的负载。
type TypingPayload struct {
	Typing bool `json:"typing"`
}

// FileOfferPayload 是 FILE_OFFER 的负载（方案 §5.2/§5.3）。
// Candidates 为空表示发送方不可被拨入，请求接收方反向拨号（M3）。
// ECDHPub 是发送方一次性 X25519 公钥（base64，32B），供端到端协商文件密钥。
type FileOfferPayload struct {
	TransferID string   `json:"transferID"`
	Name       string   `json:"name"`
	Size       int64    `json:"size"`
	ChunkSize  int      `json:"chunkSize"`
	SHA256     string   `json:"sha256"`
	Candidates []string `json:"candidates,omitempty"`
	Token      string   `json:"token,omitempty"`
	ECDHPub    string   `json:"ecdhPub,omitempty"`
}

// FileAcceptPayload 是 FILE_ACCEPT 的负载。
type FileAcceptPayload struct {
	TransferID string `json:"transferID"`
}

// FileRejectPayload 是 FILE_REJECT 的负载。
type FileRejectPayload struct {
	TransferID string `json:"transferID"`
	Reason     string `json:"reason,omitempty"`
}

// FileCancelPayload 是 FILE_CANCEL 的负载。
type FileCancelPayload struct {
	TransferID string `json:"transferID"`
}

// FileReversePayload 是 FILE_REVERSE 的负载（M3 反向拨号）。
// 接收方监听 P2P 端口后，把自己的候选地址回给不可拨入的发送方，
// 由发送方反向拨入；数据面仍是拨号方发 HELLO，逻辑与正向一致。
type FileReversePayload struct {
	TransferID string   `json:"transferID"`
	Candidates []string `json:"candidates,omitempty"`
	Token      string   `json:"token,omitempty"`
}

// FileProgressPayload 是 FILE_PROGRESS 的负载。
type FileProgressPayload struct {
	TransferID string `json:"transferID"`
	BytesDone  int64  `json:"bytesDone"`
	SpeedBps   int64  `json:"speedBps,omitempty"`
}

// FileDonePayload 是 FILE_DONE 的负载。
type FileDonePayload struct {
	TransferID string `json:"transferID"`
	SHA256     string `json:"sha256"`
}

// GroupOfferPayload 是 GROUP_OFFER 的负载（1:N，方案 §9.3）。
//
// Candidates/Token 为块交换数据面（P2P）新增：Candidates 是源节点块端口的
// ip:port 列表，Token 是本次分发握手用的一次性令牌。二者可缺省，旧端忽略。
type GroupOfferPayload struct {
	GroupID    string   `json:"groupID"`
	TransferID string   `json:"transferID"`
	Name       string   `json:"name"`
	Size       int64    `json:"size"`
	BlockCount int      `json:"blockCount"`
	SHA256     string   `json:"sha256"`
	Seeders    []string `json:"seeders,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Token      string   `json:"token,omitempty"`
}

// GroupJoinPayload 是 GROUP_JOIN / GROUP_LEAVE 的负载。
//
// Candidates/Token 用于数据面：接收方在 GROUP_JOIN 中回传自己的块端口候选，
// 让源（及其他成员）可反向拨入；GROUP_LEAVE 时可缺省。
type GroupJoinPayload struct {
	GroupID    string   `json:"groupID"`
	TransferID string   `json:"transferID"`
	Candidates []string `json:"candidates,omitempty"`
	Token      string   `json:"token,omitempty"`
}

// GroupProgressPayload 是 GROUP_PROGRESS 的负载（块位图）。
type GroupProgressPayload struct {
	GroupID    string          `json:"groupID"`
	TransferID string          `json:"transferID"`
	HaveBitmap json.RawMessage `json:"haveBitmap,omitempty"`
}

// GroupKeyPayload 是 GROUP_KEY 的负载：源把本次群文件对称密钥（base64，
// 32 字节 AES-256 密钥）经信令单播给新加入成员。群文件信令本身经
// WSS/PSK 通道保护；持有该密钥是参与数据面加解密的前提，离开分发后
// 成员不再收到后续会话密钥。
type GroupKeyPayload struct {
	GroupID    string `json:"groupID"`
	TransferID string `json:"transferID"`
	FileKey    string `json:"fileKey"`
}

// Channel 是 G2 自定义频道（方案 §9.6）。
//
// Private/Topic 为 G2 补全新增字段，均带 omitempty，旧端可缺省解析。
type Channel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	OwnerID string   `json:"ownerID"`
	Private bool     `json:"private,omitempty"`
	Topic   string   `json:"topic,omitempty"`
	Members []string `json:"members,omitempty"`
}

// ChannelCreatePayload 是 CHANNEL_CREATE 的负载。
type ChannelCreatePayload struct {
	Name    string `json:"name"`
	Private bool   `json:"private,omitempty"`
	Topic   string `json:"topic,omitempty"`
}

// ChannelJoinPayload 是 CHANNEL_JOIN 的负载。
type ChannelJoinPayload struct {
	ChannelID string `json:"channelID"`
}

// ChannelInvitePayload 是 CHANNEL_INVITE 的负载：owner 邀请 MemberID 加入。
type ChannelInvitePayload struct {
	ChannelID string `json:"channelID"`
	MemberID  string `json:"memberID"`
}

// ChannelLeavePayload 是 CHANNEL_LEAVE 的负载。
type ChannelLeavePayload struct {
	ChannelID string `json:"channelID"`
}

// ChannelListPayload 是 CHANNEL_LIST 的负载。
type ChannelListPayload struct {
	Channels []Channel `json:"channels"`
}

// RelayRequestPayload 是 RELAY_REQUEST 的负载。
// PeerID 是对端（另一端）客户端 ID，服务器据此向双方分别下发 RELAY_GRANT。
type RelayRequestPayload struct {
	TransferID string `json:"transferID"`
	PeerID     string `json:"peerID,omitempty"`
}

// RelayGrantPayload 是 RELAY_GRANT 的负载。
// RelayAddr 是中继数据面 TCP 地址 host:port（可缺省，老端忽略）。
// PeerID 为群文件回退新增：标明本次配对的对端成员，客户端据此把 grant
// 投递到对应群任务（1:1 路径缺省，旧行为不变）。
type RelayGrantPayload struct {
	TransferID string `json:"transferID"`
	RelayID    string `json:"relayID"`
	RelayAddr  string `json:"relayAddr,omitempty"`
	PeerID     string `json:"peerID,omitempty"`
}

// RelayKeyPayload 是 RELAY_KEY 的负载。
// 端到端 X25519 方案下：接收方把自己的一次性 X25519 公钥放入 PeerPub，
// 并用 ECDH 共享密钥加密文件密钥，密文（base64）放入 WrappedKey；
// 中继/中心节点只能看到封装后的密文，无法还原文件密钥。
type RelayKeyPayload struct {
	RelayID    string `json:"relayID"`
	TransferID string `json:"transferID,omitempty"`
	PeerPub    string `json:"peerPub,omitempty"`
	WrappedKey string `json:"wrappedKey,omitempty"`
}

// MsgAckPayload 是 MSG_ACK 的负载。
type MsgAckPayload struct {
	Status string `json:"status,omitempty"`
}

// ServerShutdownPayload 是 SERVER_SHUTDOWN 的负载。
type ServerShutdownPayload struct {
	GracePeriodSec int `json:"gracePeriodSec"`
}
