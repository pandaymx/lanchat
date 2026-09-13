package libsql

// mesh_sync.go 追加 libsql 的 per-source 游标与幂等去重方法（ADR-014 wire v2）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// MaxSeqOfNode 返回某源节点已落库的最大 ServerSeq；无消息返回 0。
//
// mesh 同步游标：接收方记录「源节点 X 我已收到 seq<=C」，
// 下次同步从 C+1 开始拉。
func (s *Store) MaxSeqOfNode(ctx context.Context, nodeID string) (uint64, error) {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(server_seq) FROM messages WHERE node_id = ?`, nodeID).
		Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("libsql: max seq of node %q: %w", nodeID, err)
	}
	if !maxSeq.Valid || maxSeq.Int64 < 0 {
		return 0, nil
	}
	return uint64(maxSeq.Int64), nil
}

// SyncMessages 返回源节点 nodeID 在 seq>after 之后的消息（跨会话），
// 按 ServerSeq 升序，最多 limit 条。limit<=0 取硬上限（防 OOM）。
//
// mesh 增量同步的主查询：游标推进 + 批处理分页都走它。
func (s *Store) SyncMessages(ctx context.Context, nodeID string, after uint64, limit int) ([]protocol.StoredMessage, error) {
	if limit <= 0 || limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_nonce, conv_id, sender_user, sender_device, body, server_seq, created_at,
		        file_id, file_name, file_size, file_mime, reply_to, node_id
		 FROM messages
		 WHERE node_id = ? AND server_seq > ?
		 ORDER BY server_seq ASC
		 LIMIT ?`,
		nodeID, int64(after), limit)
	if err != nil {
		return nil, fmt.Errorf("libsql: sync node %q after %d: %w", nodeID, after, err)
	}
	defer func() { _ = rows.Close() }()
	return scanMessages(rows)
}

// AppendSyncedMessage 幂等写入一条同步来的消息（ADR-014 wire v2）。
//
// 去重键 (node_id, server_seq)：已存在则静默跳过（同步可能重复送达）；
// 并发双插由 idx_messages_node_seq 唯一索引兜底，冲突时忽略。
// 与 AppendMessage 的区别：本地消息走 upsert（乐观 seq=0 → hub 回
// seq=N 覆盖）；同步消息只进一次，不改已落库的本地消息。
func (s *Store) AppendSyncedMessage(ctx context.Context, m protocol.StoredMessage) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMillis()
	}
	var fileID, fileName, fileMime string
	var fileSize int64
	if m.File != nil {
		fileID, fileName, fileSize, fileMime = m.File.FileID, m.File.Name, m.File.Size, m.File.Mime
	}
	replyJSON := ""
	if m.ReplyTo != nil {
		if b, err := json.Marshal(m.ReplyTo); err == nil {
			replyJSON = string(b)
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages
		   (conv_id, id, server_seq, client_nonce, sender_user, sender_device, body, created_at,
		    file_id, file_name, file_size, file_mime, reply_to, node_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ConversationID, m.ID, int64(m.ServerSeq), m.ClientNonce,
		m.SenderUserID, m.SenderDeviceID, m.Body, m.CreatedAt,
		fileID, fileName, fileSize, fileMime, replyJSON, m.NodeID)
	if err != nil {
		return fmt.Errorf("libsql: append synced message %q/%d: %w", m.NodeID, m.ServerSeq, err)
	}
	return nil
}
