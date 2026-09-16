package hubapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// TestE2EKeysAPI：注册 → 查询（单/多、未记录跳过）。
func TestE2EKeysAPI(t *testing.T) {
	st := memory.New()
	api := NewE2EKeysAPI(st)
	mux := http.NewServeMux()
	api.Routes(mux)

	// 注册。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2e/keys",
		strings.NewReader(`{"device_id":"dev-1","pubkey":"AAAA"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("register code = %d, body=%s", w.Code, w.Body.String())
	}
	// 缺 device_id 拒绝。
	req = httptest.NewRequest(http.MethodPost, "/api/v1/e2e/keys",
		strings.NewReader(`{"pubkey":"BBBB"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty device id code = %d", w.Code)
	}
	// 查询：存在的返回、未记录跳过。
	req = httptest.NewRequest(http.MethodGet, "/api/v1/e2e/keys?device_id=dev-1,dev-2,dev-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("lookup code = %d", w.Code)
	}
	var resp struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Keys) != 1 || resp.Keys["dev-1"] != "AAAA" {
		t.Fatalf("keys = %v", resp.Keys)
	}
	// 吊销：DELETE 后查询为空；缺 device_id 拒绝；重复删除幂等。
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/e2e/keys?device_id=dev-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke code = %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/e2e/keys", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("revoke without device_id code = %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/e2e/keys?device_id=dev-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if got := w.Body.String(); !strings.Contains(got, `"keys":{}`) {
		t.Fatalf("after revoke lookup = %s", got)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/e2e/keys?device_id=dev-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-revoke code = %d", w.Code)
	}
}
