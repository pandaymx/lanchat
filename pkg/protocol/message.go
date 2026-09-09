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
	// ReplyTo 非空表示该消息是引用回复（v1.1）。快照由发送端构造
	//（被引用消息的 ID + 发送者 + 正文截断预览），接收端渲染引用块
	// 无需再查库。Hub 原样透传；旧客户端忽略该字段（向后兼容）。
	ReplyTo *ReplyRef `json:"r,omitempty"`
}

// ReplyRef 是引用回复的引用快照（v1.1）。
//
// 发送端构造：ID 是被引用消息的服务端 ID；SenderUserID 是被引用消息
// 的发送者；Body 是被引用消息正文的截断预览（渲染用，不保证全文）。
// Hub 不校验引用是否存在（局域网信任模型同正文）。
type ReplyRef struct {
	ID           string `json:"id"`
	SenderUserID string `json:"suid"`
	Body         string `json:"body,omitempty"`
}

// SearchRequest 是历史搜索请求（FKSearchReq，v1.1）。
//
// Query 是关键词（子串匹配，大小写不敏感）；ConversationID 为空表示
// 搜全部（hub 侧按会话权限收敛：群仅成员可见，大厅全员可见）；
// Limit<=0 时 hub 取默认上限（50）。
type SearchRequest struct {
	Query          string `json:"q"`
	ConversationID string `json:"conv,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// SearchResponse 是搜索结果（FKSearchResp，v1.1）。
//
// Hits 按 ServerSeq 降序（最新在前）；调用方按 ConversationID 分组渲染。
type SearchResponse struct {
	Hits []StoredMessage `json:"hits"`
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

// FileMeta 是 hub 持久化的文件元信息（M9）。
//
// 与 FileRef 的差异：FileMeta 是存储层记录（含上传时间，供未来清理与
// 审计），FileRef 是消息里携带的展示快照（不含时间）。hub 重启后凭
// FileID 从 store 读回 FileMeta，下载服务与消息渲染都不依赖内存状态。
type FileMeta struct {
	FileID    string `json:"fid"`
	Name      string `json:"n"`
	Size      int64  `json:"sz"`
	Mime      string `json:"m,omitempty"`
	CreatedAt int64  `json:"at,omitzero"`
}

// Conversation 是一个聊天会话，kind 决定它是 DM、群聊还是频道。
type Conversation struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "dm" | "group" | "channel"
	Title string `json:"title,omitempty"`
}

// ConversationSnapshot 是 Hub → Client 的会话快照（M12-A 群聊）。
//
// 两种形态：
//   - 大厅（lobby）：Kind=="lobby"、Members 为 nil。成员是隐式的——
//     所有已握手用户都在大厅，不落库、不下发成员名单（在线名单本身
//     由 Presence 覆盖）。
//   - 群（group）：Kind=="group"、Members 为创建/邀请时的用户 ID 集合。
//
// 握手后 Hub 用 FKConvList 下发全量快照；之后每个变更用 FKConvEvent
// 增量广播。客户端（Web/TUI）按 ID upsert 即可，无需自行合并历史。
type ConversationSnapshot struct {
	Conversation Conversation `json:"c"`
	Members      []string     `json:"m,omitempty"` // 群成员 UserID；大厅为 nil
}

// ConversationRequest 是创建群的请求（FKConvCreate）。
type ConversationRequest struct {
	Title     string   `json:"t"`
	MemberIDs []string `json:"m,omitempty"`
}

// ConversationRef 是会话操作引用（FKConvInvite / FKConvLeave）。
type ConversationRef struct {
	ConversationID string   `json:"c"`
	UserIDs        []string `json:"u,omitempty"` // 仅邀请时使用
}

// ConversationEvent 是会话变更广播（FKConvEvent，M12-A）。
//
// Event 取值：
//   - "created"：新群建成（发给全部初始成员）；
//   - "joined"：有人被邀请进群（发给群内成员；ByUserID 为邀请者）；
//   - "left"：有人退群（发给群内剩余成员；ByUserID 为退出者）。
type ConversationEvent struct {
	Conversation Conversation `json:"c"`
	Event        string       `json:"e"`
	ByUserID     string       `json:"u,omitempty"`
	// Members 是事件发生后的全量成员 UserID（created/joined 时填充）。
	// left 事件不含（接收方是剩余成员，自己的快照已含该信息）。
	Members []string `json:"m,omitempty"`
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
