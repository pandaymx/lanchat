package protocol

// sync.go 定义 mesh 同步帧载荷（ADR-014 wire v2）。
//
// 去中心化模型下没有全局 hub：每个节点本地全量存储，节点间按
// per-source 游标增量同步。游标粒度 = 源节点（跨会话），去重键 =
// (NodeID, ServerSeq)。

// SyncRequest 是 mesh 同步请求（FKSyncReq 载荷）。
//
// Cursor：源节点 ID → 我已收到的该节点最大消息 seq（跨会话）。请求方
// 声明「源节点 X 的 seq<=C[X] 的消息我已经有了」，对方只需补发
// 之后的。缺省/空 map 表示「请给我全部」。
// ConvCursor 同构：源节点 ID → 我已收到的该节点最大会话事件 seq
// （M-c 群成员一致性）。两类数据各自独立游标、同一批同步。
// Presence 是本节点当前在线用户快照（M-c presence 广播）——易失状态，
// 每次同步顺带交换，接收方以最新快照覆盖，无需游标。
type SyncRequest struct {
	Cursor map[string]uint64 `json:"c,omitempty"`
	// ConvCursor 是会话事件（建群/邀请/退群）的 per-source 游标。
	ConvCursor map[string]uint64 `json:"cc,omitempty"`
	// Presence 是请求方节点的在线用户快照（实时同步用，非增量）。
	Presence []Presence `json:"p,omitempty"`
	// Limit 本次响应条数上限；<=0 由接收方取默认。
	Limit int `json:"n,omitzero"`
}

// SyncResponse 是同步响应（FKSyncResp 载荷）。
//
// From 是这批数据的源节点 ID；Messages 按 ServerSeq 升序；
// ConvEvents 按该源节点的 seq 升序（会话事件，M-c）；More=true 表示
// 消息还有后续（调用方以本批最大 seq 更新游标后继续拉）。
// Presence 字段同理：响应方节点的在线用户快照，随同步返回。
type SyncResponse struct {
	From       string           `json:"from"`
	Messages   []StoredMessage  `json:"m,omitempty"`
	ConvEvents []ConvEventEntry `json:"ce,omitempty"`
	Presence   []Presence       `json:"p,omitempty"`
	More       bool             `json:"more,omitempty"`
}

// ConvEventEntry 是一条带幂等坐标的会话事件（M-c 群成员一致性）。
// Seq 是源节点的事件序号；请求侧用本批最大 Seq 推进 per-source 游标，
// 并把事件按原 (From, Seq) 坐标落库（全量复制模型）。
type ConvEventEntry struct {
	Seq   uint64            `json:"s,omitzero"`
	Event ConversationEvent `json:"e"`
}
