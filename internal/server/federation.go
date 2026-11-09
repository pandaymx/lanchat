package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
)

const (
	federationIngestPath = "/api/federation/ingest"
	federationSeenTTLMS  = 5 * 60 * 1000
	federationMaxSeen    = 4096
	federationMaxHops    = 4
)

// FederatedEnvelope 封装跨子网中心节点联邦桥接（Server Federation Bridge，v10.0.0）转发的信令信封。
type FederatedEnvelope struct {
	OriginServer string             `json:"originServer"`
	Hops         int                `json:"hops"`
	Envelope     *protocol.Envelope `json:"envelope"`
}

type federationBridge struct {
	mu           sync.Mutex
	originID     string
	secret       string
	peers        map[string]bool
	seen         map[string]int64
	inbound      atomic.Uint64
	outbound     atomic.Uint64
	dedupDropped atomic.Uint64
	httpClient   *http.Client
}

func newFederationBridge(defaultSecret string) *federationBridge {
	return &federationBridge{
		originID:   "srv-" + uuid.NewString()[:8],
		secret:     strings.TrimSpace(defaultSecret),
		peers:      make(map[string]bool),
		seen:       make(map[string]int64),
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
}

func (fb *federationBridge) setSecret(secret string) {
	fb.mu.Lock()
	fb.secret = strings.TrimSpace(secret)
	fb.mu.Unlock()
}

func normalizeFederationEndpoint(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	u = strings.TrimRight(u, "/")
	if !strings.HasSuffix(u, federationIngestPath) {
		u += federationIngestPath
	}
	return u
}

func (fb *federationBridge) addPeer(endpoint string) bool {
	norm := normalizeFederationEndpoint(endpoint)
	if norm == "" {
		return false
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.peers[norm] = true
	return true
}

func (fb *federationBridge) peerList() []string {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	out := make([]string, 0, len(fb.peers))
	for p := range fb.peers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (fb *federationBridge) hasPeers() bool {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return len(fb.peers) > 0
}

func (fb *federationBridge) markSeen(id string, nowMS int64) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if ts, exists := fb.seen[id]; exists && nowMS-ts <= federationSeenTTLMS {
		fb.dedupDropped.Add(1)
		return false
	}
	if len(fb.seen) >= federationMaxSeen {
		for k, ts := range fb.seen {
			if nowMS-ts > federationSeenTTLMS {
				delete(fb.seen, k)
			}
		}
		if len(fb.seen) >= federationMaxSeen {
			for k := range fb.seen {
				delete(fb.seen, k)
				break
			}
		}
	}
	fb.seen[id] = nowMS
	return true
}

func (fb *federationBridge) signBody(body []byte) string {
	fb.mu.Lock()
	sec := fb.secret
	fb.mu.Unlock()
	if sec == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(sec))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (fb *federationBridge) verifyBody(body []byte, sigHeader string) bool {
	fb.mu.Lock()
	sec := fb.secret
	fb.mu.Unlock()
	if sec == "" {
		return true
	}
	expected := fb.signBody(body)
	return hmac.Equal([]byte(strings.TrimSpace(sigHeader)), []byte(expected))
}

func (fb *federationBridge) forwardEnvelopeAsync(env *protocol.Envelope) {
	if fb == nil || env == nil || env.ID == "" {
		return
	}
	peers := fb.peerList()
	if len(peers) == 0 {
		return
	}
	nowMS := time.Now().UnixMilli()
	if !fb.markSeen(env.ID, nowMS) {
		return
	}
	clone := *env
	fedEnv := FederatedEnvelope{
		OriginServer: fb.originID,
		Hops:         1,
		Envelope:     &clone,
	}
	raw, err := json.Marshal(fedEnv)
	if err != nil {
		return
	}
	sig := fb.signBody(raw)
	for _, targetURL := range peers {
		u := targetURL
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
			if rErr != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-LANChat-Fed-Origin", fb.originID)
			if sig != "" {
				req.Header.Set("X-LANChat-Fed-Signature", sig)
			}
			resp, dErr := fb.httpClient.Do(req)
			if dErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					fb.outbound.Add(1)
				}
			}
		}()
	}
}

// ConfigureFederationSecret 设置跨子网中心节点联邦桥接的 HMAC-SHA256 共享鉴别密钥（v10.0.0）。
func (s *Server) ConfigureFederationSecret(secret string) {
	if s.hub != nil && s.hub.fed != nil {
		s.hub.fed.setSecret(secret)
	}
}

// AddFederationPeer 向当前中心节点注册跨子网联邦对端服务器地址（如 "http://100.105.48.87:7878"，v10.0.0）。
func (s *Server) AddFederationPeer(endpoint string) bool {
	if s.hub == nil || s.hub.fed == nil {
		return false
	}
	return s.hub.fed.addPeer(endpoint)
}

// FederationPeers 返回当前已注册的全部跨子网联邦对端服务器摄取端点列表（v10.0.0）。
func (s *Server) FederationPeers() []string {
	if s.hub == nil || s.hub.fed == nil {
		return nil
	}
	return s.hub.fed.peerList()
}

// FederationStats 返回跨子网联邦桥接的入站接收数、出站转发数与环路去重拦截数（v10.0.0）。
func (s *Server) FederationStats() (inbound, outbound, dedupDropped uint64) {
	if s.hub == nil || s.hub.fed == nil {
		return 0, 0, 0
	}
	return s.hub.fed.inbound.Load(), s.hub.fed.outbound.Load(), s.hub.fed.dedupDropped.Load()
}

func (s *Server) handleFederationIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.hub == nil || s.hub.fed == nil {
		http.Error(w, "federation unavailable", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if !s.hub.fed.verifyBody(body, r.Header.Get("X-LANChat-Fed-Signature")) {
		http.Error(w, "invalid federation signature", http.StatusUnauthorized)
		return
	}
	var fedEnv FederatedEnvelope
	if err := json.Unmarshal(body, &fedEnv); err != nil || fedEnv.Envelope == nil || strings.TrimSpace(fedEnv.Envelope.ID) == "" {
		http.Error(w, "invalid federated envelope", http.StatusBadRequest)
		return
	}
	if fedEnv.OriginServer == s.hub.fed.originID || fedEnv.Hops <= 0 || fedEnv.Hops > federationMaxHops {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"ignored":"hop_or_origin"}`))
		return
	}
	if !s.hub.fed.markSeen(fedEnv.Envelope.ID, time.Now().UnixMilli()) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"dedup":true}`))
		return
	}
	s.hub.fed.inbound.Add(1)
	s.hub.submit(hubEvent{
		kind: evFederatedDeliver,
		env:  fedEnv.Envelope,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (h *hub) deliverFederated(env *protocol.Envelope) {
	if env == nil {
		return
	}
	h.messagesRouted++
	switch env.Group {
	case protocol.GroupUnicast:
		if env.To == "" {
			return
		}
		if target, ok := h.reg.get(env.To); ok {
			if !target.enqueue(env) {
				h.drop(target)
			}
			return
		}
		if h.spool != nil && h.spool.enqueueUser(env.To, env, h.now()) {
			h.offlineQueued++
		}
	case protocol.GroupBroadcast:
		if h.spool != nil {
			h.spool.appendChannel(protocol.GroupBroadcast, env, h.now())
		}
		_, users := h.reg.snapshot()
		for _, u := range users {
			if u.ID == env.From {
				continue
			}
			if target, ok := h.reg.get(u.ID); ok {
				if !target.enqueue(env) {
					h.drop(target)
				}
			}
		}
	default:
		if h.spool != nil {
			h.spool.appendChannel(env.Group, env, h.now())
		}
		_, users := h.reg.snapshot()
		for _, u := range users {
			if u.ID == env.From {
				continue
			}
			if target, ok := h.reg.get(u.ID); ok {
				if !target.enqueue(env) {
					h.drop(target)
				}
			}
		}
	}
}
