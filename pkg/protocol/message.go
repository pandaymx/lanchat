package protocol

// StoredMessage 是 Hub 已持久化的一条消息的完整形态。
//
// 这就是发到客户端看到的东西 —— 线缆上不存在"半生不熟"的中间态。
// 即：
//   - ServerSeq 由 Hub 分配，全局单调递增。M1 不要求严格连续，只要求单调。
//   - ClientNonce 由发送设备生成，Hub 必须保留。投递回环、重复检测、ack 跟踪都靠它。
//   - CreatedAt 是 Unix 毫秒，Hub 接收时间（不是设备本地时间）。
type StoredMessage struct {
	ID             string `json:"id"`          // 服务端去重键（ClientNonce + ServerSeq 派生）
	ClientNonce    string `json:"nonce"`       // 客户端去重用
	ConversationID string `json:"conv"`        // 属于哪个会话
	SenderUserID   string `json:"suid"`        // 发送者用户
	SenderDeviceID string `json:"sdid"`        // 发送者设备
	Body           string `json:"body"`        // 纯文本（M4 可扩展到 MIME）
	ServerSeq      uint64 `json:"seq"`         // Hub 单调递增
	CreatedAt      int64  `json:"at,omitzero"` // Unix 毫秒；零值省略便于显示
	// File 非空表示该消息携带一个文件附件（M9）。附件数据面走 hub 的
	// HTTP 端点（POST/GET /api/files），FileID 由 hub 生成、消息里只带
	// 引用与展示用元信息；历史补发 / 已读 / 去重全走消息原有管线。
	File *FileRef `json:"f,omitempty"`
}

// FileRef 是消息携带的文件附件引用（M9）。
//
// 传输流程：
//   - 发送方先把文件上传到 hub（HTTP multipart，POST /api/files），
//     拿到 hub 分配的 FileID，再发一条带 FileRef 的普通消息；
//   - 接收方用 FileID 从 hub 拉取（GET /api/files/{fileID}）。
//
// 安全边界：
//   - FileID 由 hub 用 crypto/rand 生成（32 hex），不接受客户端指定——
//     存储路径永远是 files/<FileID>，文件名/路径穿越无从谈起；
//   - Name 仅用于展示，绝不拼进文件系统路径；Mime 用于 Web 端渲染
//     决策（image/* 内联预览）。
type FileRef struct {
	FileID string `json:"fid"`
	Name   string `json:"n"`
	Size   int64  `json:"sz"`
	Mime   string `json:"m,omitempty"`
}

// Conversation 是一个聊天会话，kind 决定它是 DM、群聊还是频道。
type Conversation struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "dm" | "group" | "channel"
	Title string `json:"title,omitempty"`
}

// User 是人类用户。同一用户可以登录到多个设备（见 Device.UserID）。
// AvatarSeed 用于在没有头像上传时生成确定性 identicon。
type User struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AvatarSeed string `json:"avatar,omitempty"`
}

// Device 是单个客户端实例。一台物理机器可以有多个。
// 同 UserID 多 Device 共存的设计是**为了支持多设备同时在线**——
// 不能像 Slack/Discord 那样强行踢下线（用户明确诉求）。
// 见 AGENTS.md ADR-008。
type Device struct {
	ID     string `json:"id"`
	UserID string `json:"user"`
	Name   string `json:"name"`
}

// ReadCursor 是某设备在某个会话上"已读到哪一条"的游标，用于：
//   - 未读数显示
//   - 离线补发请求的起点（ResumeFrom）
//   - 多设备独立维护（每个 device 各自一个 cursor）
//   - 已读回执广播（M8.1）：Hub 把盖戳后的游标广播给其他人
type ReadCursor struct {
	// UserID 仅在 Hub→Client 的已读回执帧里由 Hub 盖戳填写；
	// 客户端上发用 Read（只有 c/s），无权声明身份。
	UserID         string `json:"u,omitempty"`
	DeviceID       string `json:"d"`
	ConversationID string `json:"c"`
	ServerSeq      uint64 `json:"s"`
}
