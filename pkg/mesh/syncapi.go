package mesh

// syncapi.go 是 mesh 同步的 HTTP 接入层（ADR-014 wire v2）。
//
// 所有 mesh 端点都走传输加密（Envelope，见 envelope.go）：
//   - GET  /api/v1/mesh/pubkey：明文返回本节点公钥（base64）——TOFU
//     首连时取对端公钥用，之后进入加密同步
//   - POST /api/v1/mesh/sync：Body=Envelope{Key,Nonce,Cipher}，
//     明文为 SyncRequest JSON；响应同样封装为 Envelope，明文为
//     []SyncResponse JSON
//
// 信任模型：TOFU——客户端按 URL、服务端按来源 IP 记录对端公钥，
// 首次信任、变化拒绝。局域网免鉴权模型不变（见 ADR-014）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// MeshPath 是 mesh 同步端点的路由（挂在与 WS 同端口的 mux 上）。
const MeshPath = "/api/v1/mesh/sync"

// PubKeyPath 是 mesh 公钥端点（TOFU 首连取公钥）。
const PubKeyPath = "/api/v1/mesh/pubkey"

// httpClient 是可替换的 HTTP 客户端（测试注入短超时用）。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// SyncHandler 是 mesh 同步的 HTTP 服务端：解密 Envelope → Respond →
// 加密响应。挂载：
//
//	tr.WithHandler("POST "+mesh.MeshPath, mesh.SyncHandler{Store: store, ID: id, Known: k})
type SyncHandler struct {
	Store SourceStore
	// ID 是本节点身份（加密/解密用）。
	ID *Identity
	// Known 是 TOFU 公钥表；nil 时跳过记录（纯内存模式）。
	Known *KnownKeys
}

// ServeHTTP 实现 http.Handler（method+path 由 mux 匹配）。
func (h SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env Envelope
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&env); err != nil {
		http.Error(w, "bad envelope: "+err.Error(), http.StatusBadRequest)
		return
	}
	if h.ID == nil {
		http.Error(w, "mesh encryption not configured", http.StatusInternalServerError)
		return
	}
	// TOFU：按来源 IP 记录/校验发送方公钥。
	if h.Known != nil {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if err := h.Known.Trust(host, env.Key); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	plain, err := Open(h.ID, &env)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusForbidden)
		return
	}
	var req protocol.SyncRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		http.Error(w, "bad sync request: "+err.Error(), http.StatusBadRequest)
		return
	}
	resps, err := Respond(r.Context(), h.Store, req)
	if err != nil {
		http.Error(w, "sync failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(resps)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	respEnv, err := Seal(h.ID, env.Key, body)
	if err != nil {
		http.Error(w, "encrypt failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(respEnv); err != nil {
		return
	}
}

// PubKeyHandler 明文返回本节点公钥（TOFU 首连用）。
type PubKeyHandler struct {
	ID *Identity
}

// ServeHTTP 实现 http.Handler。
func (h PubKeyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.ID == nil {
		http.Error(w, "mesh encryption not configured", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString(h.ID.PublicKey()))
}

// SyncPeer 是 mesh 同步的 HTTP 客户端：TOFU 取/校公钥 → 加密请求 →
// 发往远端 → 解密响应。
func SyncPeer(ctx context.Context, baseURL string, id *Identity, known *KnownKeys, req protocol.SyncRequest) ([]protocol.SyncResponse, error) {
	peerPub := known.PeerKey(baseURL)
	if peerPub == nil {
		// 首连：先 GET 对端公钥并 TOFU 记录。
		pub, err := fetchPeerPubKey(ctx, baseURL)
		if err != nil {
			return nil, err
		}
		if err := known.Trust(baseURL, pub); err != nil {
			return nil, err
		}
		peerPub = pub
	}

	plain, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mesh: marshal sync request: %w", err)
	}
	env, err := Seal(id, peerPub, plain)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("mesh: marshal envelope: %w", err)
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

	var respEnv Envelope
	if err := json.NewDecoder(resp.Body).Decode(&respEnv); err != nil {
		return nil, fmt.Errorf("mesh: decode envelope: %w", err)
	}
	// TOFU 一致性：响应必须来自我们信任的公钥。
	if !bytes.Equal(respEnv.Key, peerPub) {
		return nil, fmt.Errorf("mesh: peer %s responded with unexpected key (TOFU violation)", baseURL)
	}
	respPlain, err := Open(id, &respEnv)
	if err != nil {
		return nil, err
	}
	var out []protocol.SyncResponse
	if err := json.Unmarshal(respPlain, &out); err != nil {
		return nil, fmt.Errorf("mesh: decode sync response: %w", err)
	}
	return out, nil
}

// fetchPeerPubKey GET 远端公钥端点。
func fetchPeerPubKey(ctx context.Context, baseURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+PubKeyPath, nil)
	if err != nil {
		return nil, fmt.Errorf("mesh: new pubkey request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mesh: fetch peer pubkey %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mesh: fetch peer pubkey %s: status %d", baseURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return nil, fmt.Errorf("mesh: read peer pubkey: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(pub) != 32 {
		return nil, fmt.Errorf("mesh: invalid peer pubkey from %s", baseURL)
	}
	return pub, nil
}
