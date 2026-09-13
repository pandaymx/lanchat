package mesh

// syncapi.go 是 mesh 同步的 HTTP 接入层（ADR-014 wire v2）。
//
// 同步是请求-响应模型（无需 WebSocket 推送），用普通 HTTP POST 承载：
//   - 服务端：POST {base}/api/v1/mesh/sync，Body=SyncRequest JSON，
//     返回 []SyncResponse JSON（各源节点增量）
//   - 客户端：SyncPeer 发请求并解析响应，返回给 PullOnce 落库

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// MeshPath 是 mesh 同步端点的路由（挂在与 WS 同端口的 mux 上）。
const MeshPath = "/api/v1/mesh/sync"

// httpClient 是可替换的 HTTP 客户端（测试注入短超时用）。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// SyncHandler 是 mesh 同步的 HTTP 服务端：解析 SyncRequest，
// 按本地游标生成各源节点增量返回。挂载：
//
//	tr.WithHandler("POST "+mesh.MeshPath, mesh.SyncHandler{Store: store})
type SyncHandler struct {
	Store SourceStore
}

// ServeHTTP 实现 http.Handler（method+path 由 mux 匹配，这里只做
// 编解码与 Respond）。
func (h SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req protocol.SyncRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad sync request: "+err.Error(), http.StatusBadRequest)
		return
	}
	resps, err := Respond(r.Context(), h.Store, req)
	if err != nil {
		http.Error(w, "sync failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resps); err != nil {
		// 响应已部分写出，无法再改状态码，只记日志层由调用方观察。
		return
	}
}

// SyncPeer 是 mesh 同步的 HTTP 客户端：向远端 baseURL 发一轮同步
// 请求，返回各源节点增量（由请求侧 Apply 落库）。
func SyncPeer(ctx context.Context, baseURL string, req protocol.SyncRequest) ([]protocol.SyncResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mesh: marshal sync request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+MeshPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mesh: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mesh: sync peer %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("mesh: sync peer %s: status %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out []protocol.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("mesh: decode sync response: %w", err)
	}
	return out, nil
}
