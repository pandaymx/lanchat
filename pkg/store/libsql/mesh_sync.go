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
		// 注：local_seq 是接收节点本地视图序，源节点的此值对接收方无意义
		// （接收方 AppendSyncedMessage 会强制重新分配），因此查询带出
		// 但被 scanMessages 填充后由接收方忽略。
		`SELECT id, client_nonce, conv_id, sender_user, sender_device, body, encrypted, server_seq, created_at,
		        file_id, file_name, file_size, file_mime, reply_to, node_id, local_seq
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
func (s *Store) AppendSyncedMessage(ctx context.Context, m protocol.StoredMessage) (bool, error) {
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
	// local_seq 由接收节点强制重新分配（源节点的 LocalSeq 是源节点本地
	// 视图序，对接收节点无意义）；INSERT OR IGNORE 冲突时整条忽略，
	// 子查询不执行，无副作用。
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO messages
		   (conv_id, id, server_seq, client_nonce, sender_user, sender_device, body, created_at,
		    file_id, file_name, file_size, file_mime, reply_to, node_id, local_seq)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         (SELECT COALESCE(MAX(local_seq), 0) + 1 FROM messages))`,
		m.ConversationID, m.ID, int64(m.ServerSeq), m.ClientNonce,
		m.SenderUserID, m.SenderDeviceID, m.Body, m.CreatedAt,
		fileID, fileName, fileSize, fileMime, replyJSON, m.NodeID)
	if err != nil {
		return false, fmt.Errorf("libsql: append synced message %q/%d: %w", m.NodeID, m.ServerSeq, err)
	}
	// INSERT OR IGNORE：RowsAffected=1 表示新插入，0 表示已存在（幂等跳过）。
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("libsql: append synced message %q/%d: rows affected: %w", m.NodeID, m.ServerSeq, err)
	}
	return affected > 0, nil
}

// AppendConvEvent 记录一条本节点产生的会话变更事件（建群/邀请/退群），
// 供 mesh 邻居按 per-source 游标拉取（M-c 群成员一致性）。
// nodeID 是本节点身份（事件源）；seq 在 (node_id, seq) 内单调分配
// （MAX+1）。同 (node_id, seq) 重复写入幂等忽略（同步去重）。
func (s *Store) AppendConvEvent(ctx context.Context, nodeID string, ev protocol.ConversationEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("libsql: marshal conv event: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conv_events (node_id, seq, ev_type, payload, created_at)
		 VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM conv_events WHERE node_id = ?), ?, ?, ?)`,
		nodeID, nodeID, ev.Event, string(payload), nowMillis())
	if err != nil {
		return fmt.Errorf("libsql: append conv event %q/%s: %w", nodeID, ev.Event, err)
	}
	return nil
}

// ConvEventNodes 返回本地存储中有会话事件的源节点 ID。
func (s *Store) ConvEventNodes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT node_id FROM conv_events ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("libsql: conv event nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("libsql: scan conv event node: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ConvEventCursor 返回各源节点最大会话事件 seq（请求侧游标快照）。
func (s *Store) ConvEventCursor(ctx context.Context) (map[string]uint64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, MAX(seq) FROM conv_events GROUP BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("libsql: conv event cursor: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]uint64)
	for rows.Next() {
		var n string
		var mx int64
		if err := rows.Scan(&n, &mx); err != nil {
			return nil, fmt.Errorf("libsql: scan conv event cursor: %w", err)
		}
		out[n] = uint64(mx)
	}
	return out, rows.Err()
}

// StoreSyncedConvEvent 把一条 mesh 同步来的会话事件按原 (node_id, seq)
// 坐标落库（全量复制模型：每节点存所有源的事件，游标才能跨节点推进）。
// 与 AppendConvEvent 的区别：seq 由源节点分配，这里原样保留；
// 同 (node_id, seq) 重复同步幂等忽略。
func (s *Store) StoreSyncedConvEvent(ctx context.Context, nodeID string, seq uint64, ev protocol.ConversationEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("libsql: marshal synced conv event: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conv_events (node_id, seq, ev_type, payload, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		nodeID, int64(seq), ev.Event, string(payload), nowMillis())
	if err != nil {
		return fmt.Errorf("libsql: store synced conv event %q/%d: %w", nodeID, seq, err)
	}
	return nil
}

// ListConvEvents 返回某源节点 after 之后的会话事件（按 seq 升序，
// 含幂等坐标 seq，最多 limit 条）。limit<=0 时返回全部（调用方负责设上限）。
func (s *Store) ListConvEvents(ctx context.Context, nodeID string, after uint64, limit int) ([]protocol.ConvEventEntry, error) {
	q := `SELECT seq, payload FROM conv_events WHERE node_id = ? AND seq > ? ORDER BY seq ASC`
	args := []any{nodeID, int64(after)}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("libsql: list conv events %q/%d: %w", nodeID, after, err)
	}
	defer func() { _ = rows.Close() }()
	var out []protocol.ConvEventEntry
	for rows.Next() {
		var seq int64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, fmt.Errorf("libsql: scan conv event: %w", err)
		}
		var ev protocol.ConversationEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, fmt.Errorf("libsql: unmarshal conv event: %w", err)
		}
		out = append(out, protocol.ConvEventEntry{Seq: uint64(seq), Event: ev})
	}
	return out, rows.Err()
}

// GetSyncedMessage 按 (node_id, server_seq) 取回落库后的权威消息
// （含接收节点分配的 LocalSeq），供 mesh_loop 广播闭环使用。
func (s *Store) GetSyncedMessage(ctx context.Context, nodeID string, serverSeq uint64) (protocol.StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_nonce, conv_id, sender_user, sender_device, body, encrypted, server_seq, created_at,
		        file_id, file_name, file_size, file_mime, reply_to, node_id, local_seq
		 FROM messages WHERE node_id = ? AND server_seq = ?`,
		nodeID, int64(serverSeq))
	if err != nil {
		return protocol.StoredMessage{}, fmt.Errorf("libsql: get synced message %q/%d: %w", nodeID, serverSeq, err)
	}
	defer func() { _ = rows.Close() }()
	msgs, err := scanMessages(rows)
	if err != nil {
		return protocol.StoredMessage{}, err
	}
	if len(msgs) != 1 {
		return protocol.StoredMessage{}, fmt.Errorf("libsql: get synced message %q/%d: got %d rows", nodeID, serverSeq, len(msgs))
	}
	return msgs[0], nil
}
