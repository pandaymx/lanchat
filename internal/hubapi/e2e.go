package hubapi

// e2e.go 是 E2E 密钥管理（keyring）的 HTTP 端点。
//
// 局域网信任模型：hub 只存公钥目录、不分发私钥。公钥公开不泄密，
// 因此端点不做额外鉴权（与 files API 一致；配 Token 的部署由
// hub 传输层整体保护）。
//
//	POST /api/v1/e2e/keys  {"device_id":"...","pubkey":"<base64>"}
//	GET  /api/v1/e2e/keys  ?device_id=a,b,c → {"keys":{"a":"<base64>",...}}
//
// 客户端发送 E2E 消息前查接收方设备公钥；启动时也可主动注册
//（消息自我声明是主要路径，这里提供显式注册/查询入口）。

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// E2EKeysAPI 是 E2E 公钥注册/查询端点。
type E2EKeysAPI struct {
	store  core.Store
	logger *logging.ComponentLogger
}

// NewE2EKeysAPI 构造 E2E keyring API。
func NewE2EKeysAPI(store core.Store) *E2EKeysAPI {
	return &E2EKeysAPI{store: store, logger: logging.New("hub.api.e2e")}
}

// Routes 挂载 E2E 端点（Go 1.22 method+path pattern）。
func (a *E2EKeysAPI) Routes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/e2e/keys", a)
	mux.Handle("GET /api/v1/e2e/keys", a)
}

// ServeHTTP 分发注册与查询。
func (a *E2EKeysAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.handleRegister(w, r)
	case http.MethodGet:
		a.handleLookup(w, r)
	default:
		w.Header().Set("Allow", "POST, GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// registerRequest 是注册请求体。
type registerRequest struct {
	DeviceID string `json:"device_id"`
	Pubkey   string `json:"pubkey"`
}

// handleRegister 记录设备 E2E 公钥（幂等覆盖）。
func (a *E2EKeysAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DeviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}
	if req.Pubkey == "" {
		http.Error(w, "pubkey required", http.StatusBadRequest)
		return
	}
	if err := a.store.SaveE2EKey(r.Context(), req.DeviceID, req.Pubkey); err != nil {
		a.logger.Error("e2e register failed", "dev", req.DeviceID, "err", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleLookup 批量查询设备 E2E 公钥（逗号分隔的 device_id）。
func (a *E2EKeysAPI) handleLookup(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("device_id"), ",")
	keys := map[string]string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		pub, err := a.store.GetE2EKey(r.Context(), id)
		if err != nil {
			a.logger.Error("e2e lookup failed", "dev", id, "err", err)
			http.Error(w, "lookup failed", http.StatusInternalServerError)
			return
		}
		if pub != "" {
			keys[id] = pub
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}
