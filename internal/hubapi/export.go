// Package hubapi 提供 hub 的 HTTP 数据面端点（与 WS 同端口）。
//
// 备份导出端点（v1.2）：GET /api/export 返回全量 JSON 备份。无鉴权，
// 与 WS/文件端点现状一致（局域网 MVP）；导出内容含全部会话消息，
// 属于敏感数据，部署在不可信网络时应在前置加认证。
package hubapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// ExportAPI 提供 GET /api/export 全量备份下载。
type ExportAPI struct {
	store  core.Store
	logger *logging.ComponentLogger
}

// NewExportAPI 构造导出端点。store 必填（实现 core.Store 即可，
// libsql / memory 均可）。
func NewExportAPI(store core.Store) *ExportAPI {
	return &ExportAPI{store: store, logger: logging.New("hub.api")}
}

// Routes 挂载导出端点。
func (a *ExportAPI) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/export", a)
}

// ServeHTTP 实现 http.Handler：GET 时下发备份 JSON，其余 405。
func (a *ExportAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := a.store.ExportAll(r.Context())
	if err != nil {
		a.logger.Error("export failed", "err", err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	if b == nil {
		http.Error(w, "export produced no data", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=lanchat-backup-%s.json",
			time.UnixMilli(b.ExportedAt).Format("20060102-150405")))
	if err := json.NewEncoder(w).Encode(b); err != nil {
		a.logger.Warn("export encode failed", "err", err)
	}
}
