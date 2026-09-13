package mesh

// mesh.go 定义去中心化 mesh 同步引擎（ADR-014 wire v2）。
//
// 模型：没有中心 hub，每个节点本地全量存储，节点间按 per-source
// 游标增量同步，最终收敛到全量一致。同步是「拉」模型：
//   - 请求侧：本地 SourceCursor 快照 → SyncRequest{Cursor} 发给远端
//   - 远端侧：枚举自己的源节点，对每个源 X 补发 cursor[X] 之后的增量
//   - 请求侧：AppendSyncedMessage 幂等落库（去重键 (node_id, seq)）
//
// 本文件只含纯逻辑（不绑定传输）；网络接入（WS 帧/mDNS 发现）由
// pkg/meshnet 提供，引擎通过 Remote 接口驱动。

import (
	"context"
	"sort"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// SourceStore 是 mesh 同步引擎对本地存储的最小需求。
// libsql.Store 天然满足；memory.Store 支持 mesh 时再实现。
type SourceStore interface {
	// SourceNodes 返回本地所有非空源节点 ID（远端侧枚举用）。
	SourceNodes(ctx context.Context) ([]string, error)
	// SourceCursor 返回各源节点最大 seq（请求侧游标快照）。
	SourceCursor(ctx context.Context) (map[string]uint64, error)
	// SyncMessages 返回某源节点 after 之后的增量（升序，最多 limit 条）。
	SyncMessages(ctx context.Context, nodeID string, after uint64, limit int) ([]protocol.StoredMessage, error)
	// AppendSyncedMessage 幂等写入一条同步消息（去重键 (node_id, seq)）。
	AppendSyncedMessage(ctx context.Context, m protocol.StoredMessage) error
}

// Remote 是同步对端的最小抽象：执行一轮拉取并返回各源节点增量。
// 真实实现是 WS 帧连接（FKSyncReq/FKSyncResp）；测试用 stub。
type Remote interface {
	// Sync 发送请求（含请求侧游标）到远端，返回远端各源节点增量。
	Sync(ctx context.Context, req protocol.SyncRequest) ([]protocol.SyncResponse, error)
}

// DefaultLimit 单批消息上限；远端据此分批（More=true 继续拉）。
const DefaultLimit = 512

// Respond 是远端侧逻辑：按请求游标生成增量响应。
//
// 对本地每个源节点 X：after = req.Cursor[X]（缺省 0=全量），
// 取 SyncMessages(X, after, limit) 作为一批；若条数==limit 认为可能
// 还有后续（More=true，请求侧用本批最大 seq 更新游标后重拉）。
func Respond(ctx context.Context, store SourceStore, req protocol.SyncRequest) ([]protocol.SyncResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	nodes, err := store.SourceNodes(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(nodes) // 确定性输出（测试与日志友好）
	var out []protocol.SyncResponse
	for _, node := range nodes {
		after := req.Cursor[node]
		msgs, err := store.SyncMessages(ctx, node, after, limit)
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			continue // 该源无增量，不出响应
		}
		out = append(out, protocol.SyncResponse{
			From:     node,
			Messages: msgs,
			More:     len(msgs) == limit,
		})
	}
	return out, nil
}

// Apply 是请求侧逻辑：把远端响应幂等落库，返回落库条数。
func Apply(ctx context.Context, store SourceStore, resps []protocol.SyncResponse) (int, error) {
	pulled := 0
	for _, resp := range resps {
		for _, m := range resp.Messages {
			if err := store.AppendSyncedMessage(ctx, m); err != nil {
				return pulled, err
			}
			pulled++
		}
	}
	return pulled, nil
}

// PullOnce 执行一轮同步：本地游标 → 远端 → 落库，返回拉取条数。
// 这是引擎的主循环步进；对每个邻居周期性调用。
func PullOnce(ctx context.Context, store SourceStore, remote Remote) (int, error) {
	cursor, err := store.SourceCursor(ctx)
	if err != nil {
		return 0, err
	}
	resps, err := remote.Sync(ctx, protocol.SyncRequest{Cursor: cursor, Limit: DefaultLimit})
	if err != nil {
		return 0, err
	}
	return Apply(ctx, store, resps)
}
