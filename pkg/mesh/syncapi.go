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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// MeshPath 是 mesh 同步端点的路由（挂在与 WS 同端口的 mux 上）。
const MeshPath = "/api/v1/mesh/sync"

// MeshPresencePath 是 mesh presence 即时推送端点（M-c）：本地用户
// 上线/下线时把单条 presence 变化推给邻居，不等 5s 同步周期。
const MeshPresencePath = "/api/v1/mesh/presence"

// MeshFilePath 是 mesh 文件 fetch-through 端点（M-c 文件全节点同步）：
// 邻居本地无 blob 时从这里按 FileID 拉取（meta 在响应头，body 是流）。
const MeshFilePath = "/api/v1/mesh/file"

// PubKeyPath 是 mesh 公钥端点（TOFU 首连取公钥）。
const PubKeyPath = "/api/v1/mesh/pubkey"

// setHandshakeHeaders 在 mesh 请求上带设备名与配对 Token（空值跳过）。
func setHandshakeHeaders(req *http.Request, device, joinToken string) {
	if device != "" {
		req.Header.Set(HeaderDevice, device)
	}
	if joinToken != "" {
		req.Header.Set(HeaderToken, joinToken)
	}
}

// httpClient 是可替换的 HTTP 客户端（测试注入短超时用）。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// 握手请求头：随每个 mesh 请求带上本节点设备名与配对 Token。
// 服务端对未知节点校验 Token（gated TOFU），已知节点无需 Token。
const (
	HeaderDevice = "X-Lanchat-Device"
	HeaderToken  = "X-Lanchat-Token"
)

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
	// JoinHash 是配对 Token 的 SHA-256 hex；空 = 未启用配对
	// （未知节点保持旧 TOFU 行为）。非空时未知节点必须带有效
	// Token 才被授权加入（见 handshake.go）。
	JoinHash []byte
	// ApplyPresence 应用请求方节点随同步带来的在线用户快照（M-c）。
	// 由 hubserver 注入 router.ApplyRemotePresence（广播给本地客户端）。
	// nil 时静默忽略。
	ApplyPresence func([]protocol.Presence)
	// Presence 返回本节点当前在线用户快照，随响应返回给请求方。
	// nil 时响应不带 presence 数据。
	Presence func() []protocol.Presence
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
	// 身份握手：按公钥指纹鉴权（已知放行 / 未知校验配对 Token）。
	if h.Known != nil {
		if _, err := h.Known.authorizePeer(
			h.JoinHash, env.Key, r.Header.Get(HeaderDevice), r.Header.Get(HeaderToken),
		); err != nil {
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
	// M-c presence 广播：应用请求方节点的在线用户快照（周期同步路径）。
	if h.ApplyPresence != nil && len(req.Presence) > 0 {
		h.ApplyPresence(req.Presence)
	}
	resps, err := Respond(r.Context(), h.Store, h.Presence, req)
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
func SyncPeer(ctx context.Context, baseURL string, id *Identity, known *KnownKeys, req protocol.SyncRequest, device, joinToken string) ([]protocol.SyncResponse, error) {
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
	setHandshakeHeaders(httpReq, device, joinToken)

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

// PresenceHandler 是 mesh presence 即时推送的服务端（POST，Envelope 加密）：
// 邻居上线/下线时把单条 presence 变化推过来，这里应用并广播给本地客户端。
type PresenceHandler struct {
	ID *Identity
	// Known 是 TOFU 公钥表；nil 时跳过记录（纯内存模式）。
	Known *KnownKeys
	// Apply 应用一条邻居节点的 presence 变化（由 hubserver 注入）。
	Apply func(protocol.Presence)
	// JoinHash 配对 Token 哈希；语义同 SyncHandler.JoinHash。
	JoinHash []byte
}

// ServeHTTP 实现 http.Handler（method+path 由 mux 匹配）。
func (h PresenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if h.Known != nil {
		if _, err := h.Known.authorizePeer(
			h.JoinHash, env.Key, r.Header.Get(HeaderDevice), r.Header.Get(HeaderToken),
		); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	plain, err := Open(h.ID, &env)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusForbidden)
		return
	}
	var pr protocol.Presence
	if err := json.Unmarshal(plain, &pr); err != nil {
		http.Error(w, "bad presence: "+err.Error(), http.StatusBadRequest)
		return
	}
	if h.Apply != nil {
		h.Apply(pr)
	}
	w.WriteHeader(http.StatusNoContent)
}

// PushPresence 是 mesh presence 即时推送的客户端：TOFU 取/校公钥 →
// 加密单条 presence → 发往邻居端点。失败返回 error（调用方记日志即可，
// 周期同步会纠偏）。
func PushPresence(ctx context.Context, baseURL string, id *Identity, known *KnownKeys, pr protocol.Presence, device, joinToken string) error {
	peerPub := known.PeerKey(baseURL)
	if peerPub == nil {
		pub, err := fetchPeerPubKey(ctx, baseURL)
		if err != nil {
			return err
		}
		if err := known.Trust(baseURL, pub); err != nil {
			return err
		}
		peerPub = pub
	}
	body, err := json.Marshal(pr)
	if err != nil {
		return err
	}
	env, err := Seal(id, peerPub, body)
	if err != nil {
		return err
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+MeshPresencePath, bytes.NewReader(envJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setHandshakeHeaders(req, device, joinToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("mesh presence push: status %d", resp.StatusCode)
	}
	return nil
}

// FileFetchHandler 是 mesh 文件 fetch-through 的服务端（POST，Envelope
// 加密）：邻居下载文件本地未命中时，按 FileID 打开本地 blob 并流式
// 返回（meta 放 X-Mesh-File-Meta 响应头，body 为纯 blob）。
type FileFetchHandler struct {
	ID *Identity
	// Known 是 TOFU 公钥表；nil 时跳过记录（纯内存模式）。
	Known *KnownKeys
	// Open 按 FileID 打开本地文件（hubfile.Service.Open），由 hubserver 注入。
	Open func(ctx context.Context, fileID string) (protocol.FileMeta, io.ReadCloser, error)
	// JoinHash 配对 Token 哈希；语义同 SyncHandler.JoinHash。
	JoinHash []byte
}

// ServeHTTP 实现 http.Handler（method+path 由 mux 匹配）。
func (h FileFetchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env Envelope
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&env); err != nil {
		http.Error(w, "bad envelope: "+err.Error(), http.StatusBadRequest)
		return
	}
	if h.ID == nil || h.Open == nil {
		http.Error(w, "mesh file fetch not configured", http.StatusInternalServerError)
		return
	}
	if h.Known != nil {
		if _, err := h.Known.authorizePeer(
			h.JoinHash, env.Key, r.Header.Get(HeaderDevice), r.Header.Get(HeaderToken),
		); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	plain, err := Open(h.ID, &env)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusForbidden)
		return
	}
	var req struct {
		FileID string `json:"fid"`
	}
	if err := json.Unmarshal(plain, &req); err != nil || req.FileID == "" {
		http.Error(w, "bad fetch request", http.StatusBadRequest)
		return
	}
	meta, rc, err := h.Open(r.Context(), req.FileID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer func() { _ = rc.Close() }()
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		http.Error(w, "marshal meta", http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Mesh-File-Meta", base64.RawURLEncoding.EncodeToString(metaJSON))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// FetchFile 是 mesh 文件 fetch-through 的客户端：TOFU 取/校公钥 →
// 加密请求 → POST 邻居端点 → 返回 meta 与可读流（调用方负责 Close）。
// meta 从响应头解析，body 是纯 blob，流式转发不占内存。
func FetchFile(ctx context.Context, baseURL string, id *Identity, known *KnownKeys, fileID string, device, joinToken string) (protocol.FileMeta, io.ReadCloser, error) {
	peerPub := known.PeerKey(baseURL)
	if peerPub == nil {
		pub, err := fetchPeerPubKey(ctx, baseURL)
		if err != nil {
			return protocol.FileMeta{}, nil, err
		}
		if err := known.Trust(baseURL, pub); err != nil {
			return protocol.FileMeta{}, nil, err
		}
		peerPub = pub
	}
	reqJSON, err := json.Marshal(struct {
		FileID string `json:"fid"`
	}{FileID: fileID})
	if err != nil {
		return protocol.FileMeta{}, nil, err
	}
	env, err := Seal(id, peerPub, reqJSON)
	if err != nil {
		return protocol.FileMeta{}, nil, err
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return protocol.FileMeta{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+MeshFilePath, bytes.NewReader(envJSON))
	if err != nil {
		return protocol.FileMeta{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setHandshakeHeaders(req, device, joinToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return protocol.FileMeta{}, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return protocol.FileMeta{}, nil, fmt.Errorf("mesh file fetch: status %d", resp.StatusCode)
	}
	metaB64 := resp.Header.Get("X-Mesh-File-Meta")
	if metaB64 == "" {
		defer func() { _ = resp.Body.Close() }()
		return protocol.FileMeta{}, nil, errors.New("mesh file fetch: missing meta header")
	}
	metaJSON, err := base64.RawURLEncoding.DecodeString(metaB64)
	if err != nil {
		defer func() { _ = resp.Body.Close() }()
		return protocol.FileMeta{}, nil, fmt.Errorf("mesh file fetch: bad meta: %w", err)
	}
	var meta protocol.FileMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		defer func() { _ = resp.Body.Close() }()
		return protocol.FileMeta{}, nil, fmt.Errorf("mesh file fetch: bad meta json: %w", err)
	}
	return meta, resp.Body, nil
}
