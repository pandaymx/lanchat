package protocol

// sync.go 定义 mesh 同步帧载荷（ADR-014 wire v2）。
//
// 去中心化模型下没有全局 hub：每个节点本地全量存储，节点间按
// per-source 游标增量同步。游标粒度 = 源节点（跨会话），去重键 =
// (NodeID, ServerSeq)。

// SyncRequest 是 mesh 同步请求（FKSyncReq 载荷）。
//
// Cursor：源节点 ID → 我已收到的该节点最大 seq（跨会话）。请求方
// 声明「源节点 X 的 seq<=C[X] 的消息我已经有了」，对方只需补发
// 之后的。缺省/空 map 表示「请给我全部」。
type SyncRequest struct {
	Cursor map[string]uint64 `json:"c,omitempty"`
	// Limit 本次响应条数上限；<=0 由接收方取默认。
	Limit int `json:"n,omitzero"`
}

// SyncResponse 是同步响应（FKSyncResp 载荷）。
//
// From 是这批消息的源节点 ID；Messages 按 ServerSeq 升序；
// More=true 表示还有后续（调用方以本批最大 seq 更新游标后继续拉）。
type SyncResponse struct {
	From     string          `json:"from"`
	Messages []StoredMessage `json:"m,omitempty"`
	More     bool            `json:"more,omitempty"`
}
