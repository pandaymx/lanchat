package libsql

// mesh_nodes.go 提供 mesh 同步的源节点枚举（ADR-014 wire v2）。

import (
	"context"
	"fmt"
)

// SourceNodes 返回库中所有非空源节点 ID（去重）。
//
// mesh 同步的远端侧用它枚举「我有哪些源节点的消息」——对每个源节点
// 生成一批增量响应。v1 旧数据 node_id 为空串，不计入。
func (s *Store) SourceNodes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT node_id FROM messages WHERE node_id != '' ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("libsql: source nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("libsql: scan source node: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("libsql: iterate source nodes: %w", err)
	}
	return out, nil
}

// SourceCursor 返回各源节点的最大 ServerSeq（跨会话）——本地知识快照。
//
// mesh 同步的请求侧用它构建游标：对每个源节点 X，本地已收到
// seq<=Cursor[X] 的消息，远端只需补发之后的。
func (s *Store) SourceCursor(ctx context.Context) (map[string]uint64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT node_id, MAX(server_seq) FROM messages WHERE node_id != '' GROUP BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("libsql: source cursor: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]uint64{}
	for rows.Next() {
		var id string
		var maxSeq int64
		if err := rows.Scan(&id, &maxSeq); err != nil {
			return nil, fmt.Errorf("libsql: scan source cursor: %w", err)
		}
		if maxSeq > 0 {
			out[id] = uint64(maxSeq)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("libsql: iterate source cursor: %w", err)
	}
	return out, nil
}
