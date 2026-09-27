package protocol

// envelopeV 是 Envelope.V 的当前整数版本（信封结构版本）。
const envelopeV = 2

// 控制面消息类型（方案 §3.2）。
const (
	// Hello 客户端 → 服务端：登录握手。
	Hello = "HELLO"
	// Welcome 服务端 → 客户端：握手成功，下发自身信息与特性。
	Welcome = "WELCOME"
	// AuthFail 服务端 → 客户端：鉴权失败。
	AuthFail = "AUTH_FAIL"
	// Error 服务端 → 客户端：通用错误。
	Error = "ERROR"

	// UserListReq 客户端 → 服务端：请求在线用户全量表。
	UserListReq = "USER_LIST_REQ"
	// UserList 服务端 → 客户端：在线用户全量表。
	UserList = "USER_LIST"
	// UserJoin 服务端 → 客户端：有用户上线（增量）。
	UserJoin = "USER_JOIN"
	// UserLeave 服务端 → 客户端：有用户下线（增量）。
	UserLeave = "USER_LEAVE"
	// PresenceUpdate 服务端 → 客户端：用户状态变更（增量）。
	PresenceUpdate = "PRESENCE_UPDATE"

	// Heartbeat 心跳请求。
	Heartbeat = "HEARTBEAT"
	// HeartbeatAck 心跳应答。
	HeartbeatAck = "HEARTBEAT_ACK"

	// TextMsg 文本消息。
	TextMsg = "TEXT_MSG"
	// StickerMsg 表情/贴纸消息。
	StickerMsg = "STICKER_MSG"
	// Typing 正在输入提示。
	Typing = "TYPING"

	// FileOffer 文件发送要约。
	FileOffer = "FILE_OFFER"
	// FileAccept 接收方同意接收。
	FileAccept = "FILE_ACCEPT"
	// FileReject 接收方拒绝。
	FileReject = "FILE_REJECT"
	// FileCancel 取消文件传输。
	FileCancel = "FILE_CANCEL"
	// FileProgress 文件传输进度。
	FileProgress = "FILE_PROGRESS"
	// FileDone 文件传输完成。
	FileDone = "FILE_DONE"

	// GroupOffer 群组文件要约（1:N）。
	GroupOffer = "GROUP_OFFER"
	// GroupJoin 加入群组文件分发。
	GroupJoin = "GROUP_JOIN"
	// GroupLeave 离开群组文件分发。
	GroupLeave = "GROUP_LEAVE"
	// GroupProgress 群组分发进度（块位图）。
	GroupProgress = "GROUP_PROGRESS"

	// ChannelCreate G2 创建自定义频道。
	ChannelCreate = "CHANNEL_CREATE"
	// ChannelJoin G2 加入自定义频道。
	ChannelJoin = "CHANNEL_JOIN"
	// ChannelList G2 频道列表。
	ChannelList = "CHANNEL_LIST"

	// RelayRequest 请求中继。
	RelayRequest = "RELAY_REQUEST"
	// RelayGrant 中继已分配。
	RelayGrant = "RELAY_GRANT"
	// RelayKey 下发中继会话密钥。
	RelayKey = "RELAY_KEY"

	// MsgAck 消息应答。
	MsgAck = "MSG_ACK"
	// ServerShutdown 服务器即将关闭。
	ServerShutdown = "SERVER_SHUTDOWN"
)
