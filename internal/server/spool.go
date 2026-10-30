package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/internal/group"
	"github.com/pandaymx/lanchat/internal/protocol"
)

const (
	// maxSpoolPerUser 是单用户离线暂存消息环形缓冲容量上限（M16）。
	maxSpoolPerUser = 100
	// maxSpoolBytesPerUser 是单用户离线信封累计载荷字节配额上限（512 KiB，v8.1.0）。
	maxSpoolBytesPerUser = 512 * 1024
	// maxChannelHistory 是单频道保留的最近历史消息快照条数上限（M16）。
	maxChannelHistory = 50
	// spoolTTL 是离线暂存与频道历史消息的最大存活时长（24 小时）。
	spoolTTL = 24 * time.Hour
	// spoolCipherPrefix 标识中心节点磁盘 AES-256-GCM 加密快照头（v8.1.0）。
	spoolCipherPrefix = "LCSP1:"
)

type spooledEnvelope struct {
	env      *protocol.Envelope
	queuedAt time.Time
}

type persistedSpoolItem struct {
	Env      *protocol.Envelope `json:"env"`
	QueuedAt int64              `json:"queuedAt"`
}

type persistedServerState struct {
	UserQueues  map[string][]persistedSpoolItem `json:"userQueues,omitempty"`
	ChannelHist map[string][]persistedSpoolItem `json:"channelHist,omitempty"`
	Channels    []group.ChannelInfo             `json:"channels,omitempty"`
	KeyDir      map[string]string               `json:"keyDir,omitempty"`
}

// messageSpool 维护服务端单播离线暂存队列（Store-and-Forward）、频道最近历史快照（M16）、
// X25519 公钥目录以及 AES-256-GCM 加密原子落盘快照持久化（M18 / v8.1.0）。
// 所有方法均在 hub 单 goroutine 内调用，无需额外互斥锁。
type messageSpool struct {
	stateFile       string
	encKey          [32]byte
	maxPerUser      int
	maxBytesPerUser int
	ttl             time.Duration
	userQueues      map[string][]spooledEnvelope
	channelHist     map[string][]spooledEnvelope
	keyDir          map[string]string
	channelsFn      func() []group.ChannelInfo
}

func newMessageSpool(stateFile, secretSeed string, channelsFn func() []group.ChannelInfo) (*messageSpool, []group.ChannelInfo) {
	s := &messageSpool{
		stateFile:       strings.TrimSpace(stateFile),
		encKey:          deriveSpoolKey(secretSeed),
		maxPerUser:      maxSpoolPerUser,
		maxBytesPerUser: maxSpoolBytesPerUser,
		ttl:             spoolTTL,
		userQueues:      make(map[string][]spooledEnvelope),
		channelHist:     make(map[string][]spooledEnvelope),
		keyDir:          make(map[string]string),
		channelsFn:      channelsFn,
	}
	restored := s.loadState(time.Now())
	return s, restored
}

func deriveSpoolKey(seed string) [32]byte {
	return sha256.Sum256([]byte("lanchat-spool-aes256gcm-v1:" + strings.TrimSpace(seed)))
}

// setQuotaAndTTL 动态调整单用户离线信封条数上限、字节配额与过期 TTL（v8.1.0）。
func (s *messageSpool) setQuotaAndTTL(maxItems, maxBytes int, ttl time.Duration) {
	if maxItems > 0 {
		s.maxPerUser = maxItems
	}
	if maxBytes > 0 {
		s.maxBytesPerUser = maxBytes
	}
	if ttl > 0 {
		s.ttl = ttl
	}
}

// recordPubKey 记录并持久化节点的静态 X25519 公钥目录（v8.1.0）。
func (s *messageSpool) recordPubKey(userID, pubKey string, now time.Time) {
	userID = strings.TrimSpace(userID)
	pubKey = strings.TrimSpace(pubKey)
	if userID == "" || pubKey == "" {
		return
	}
	if s.keyDir[userID] == pubKey {
		return
	}
	s.keyDir[userID] = pubKey
	s.saveState(now)
}

// keyDirectory 返回当前中心节点已知的全网 X25519 公钥目录快照副本（v8.1.0）。
func (s *messageSpool) keyDirectory() map[string]string {
	if len(s.keyDir) == 0 {
		return nil
	}
	cp := make(map[string]string, len(s.keyDir))
	for k, v := range s.keyDir {
		cp[k] = v
	}
	return cp
}

// encodeKeyDirectoryFeature 将公钥目录编码为 WELCOME.features 携带的 "keydir:<b64>" 条目。
func encodeKeyDirectoryFeature(keyDir map[string]string) string {
	if len(keyDir) == 0 {
		return ""
	}
	raw, err := json.Marshal(keyDir)
	if err != nil || len(raw) == 0 {
		return ""
	}
	return "keydir:" + base64.StdEncoding.EncodeToString(raw)
}

func envelopeApproxBytes(env *protocol.Envelope) int {
	if env == nil {
		return 0
	}
	return len(env.ID) + len(env.From) + len(env.To) + len(env.Type) + len(env.Payload) + 32
}

// isSpoolableType 判定消息类型是否支持离线暂存与频道历史回放。
func isSpoolableType(typ string) bool {
	switch typ {
	case protocol.TextMsg, protocol.StickerMsg, protocol.MsgRevoke:
		return true
	default:
		return false
	}
}

// canQueueTarget 判定离线目标 ID 是否可进入离线暂存队列。
func canQueueTarget(to string) bool {
	return to != "" && to != "nonexistent"
}

// cloneEnvelope 复制信封，避免后续修改污染暂存快照。
func cloneEnvelope(env *protocol.Envelope) *protocol.Envelope {
	if env == nil {
		return nil
	}
	cp := *env
	if len(env.Payload) > 0 {
		cp.Payload = append([]byte(nil), env.Payload...)
	}
	return &cp
}

// enqueueUser 将单播消息追加到目标用户的离线环形队列（执行条数配额、字节配额与 TTL 治理）。
func (s *messageSpool) enqueueUser(userID string, env *protocol.Envelope, now time.Time) bool {
	if !canQueueTarget(userID) || !isSpoolableType(env.Type) {
		return false
	}
	limitItems := s.maxPerUser
	if limitItems <= 0 {
		limitItems = maxSpoolPerUser
	}
	limitBytes := s.maxBytesPerUser
	if limitBytes <= 0 {
		limitBytes = maxSpoolBytesPerUser
	}

	q := s.filterValid(s.userQueues[userID], now)
	q = append(q, spooledEnvelope{
		env:      cloneEnvelope(env),
		queuedAt: now,
	})
	if len(q) > limitItems {
		q = append([]spooledEnvelope(nil), q[len(q)-limitItems:]...)
	}
	for len(q) > 1 && totalQueueBytes(q) > limitBytes {
		q = q[1:]
	}
	s.userQueues[userID] = q
	s.saveState(now)
	return true
}

func totalQueueBytes(items []spooledEnvelope) int {
	total := 0
	for _, item := range items {
		total += envelopeApproxBytes(item.env)
	}
	return total
}

// drainUser 取出并清空目标用户的所有未过期离线暂存消息。
func (s *messageSpool) drainUser(userID string, now time.Time) []*protocol.Envelope {
	q, ok := s.userQueues[userID]
	if !ok || len(q) == 0 {
		return nil
	}
	delete(s.userQueues, userID)
	s.saveState(now)
	valid := s.filterValid(q, now)
	if len(valid) == 0 {
		return nil
	}
	out := make([]*protocol.Envelope, 0, len(valid))
	for _, item := range valid {
		out = append(out, cloneEnvelope(item.env))
	}
	return out
}

// appendChannel 记录频道最近 50 条消息快照（上限 50 条，TTL 24h）。
func (s *messageSpool) appendChannel(channelID string, env *protocol.Envelope, now time.Time) {
	if channelID == "" || channelID == protocol.GroupUnicast || !isSpoolableType(env.Type) {
		return
	}
	q := s.filterValid(s.channelHist[channelID], now)
	q = append(q, spooledEnvelope{
		env:      cloneEnvelope(env),
		queuedAt: now,
	})
	if len(q) > maxChannelHistory {
		q = append([]spooledEnvelope(nil), q[len(q)-maxChannelHistory:]...)
	}
	s.channelHist[channelID] = q
	s.saveState(now)
}

// channelHistory 返回指定频道未过期的最近历史消息快照列表。
func (s *messageSpool) channelHistory(channelID string, now time.Time) []*protocol.Envelope {
	q, ok := s.channelHist[channelID]
	if !ok || len(q) == 0 {
		return nil
	}
	valid := s.filterValid(q, now)
	if len(valid) == 0 {
		delete(s.channelHist, channelID)
		return nil
	}
	s.channelHist[channelID] = valid
	out := make([]*protocol.Envelope, 0, len(valid))
	for _, item := range valid {
		out = append(out, cloneEnvelope(item.env))
	}
	return out
}

// prune 清理所有已超过 TTL 的离线暂存与频道历史消息，若有变更则同步更新磁盘加密快照。
func (s *messageSpool) prune(now time.Time) {
	changed := false
	for uid, q := range s.userQueues {
		prevLen := len(q)
		valid := s.filterValid(q, now)
		if len(valid) != prevLen {
			changed = true
		}
		if len(valid) == 0 {
			delete(s.userQueues, uid)
		} else {
			s.userQueues[uid] = valid
		}
	}
	for cid, q := range s.channelHist {
		prevLen := len(q)
		valid := s.filterValid(q, now)
		if len(valid) != prevLen {
			changed = true
		}
		if len(valid) == 0 {
			delete(s.channelHist, cid)
		} else {
			s.channelHist[cid] = valid
		}
	}
	if changed {
		s.saveState(now)
	}
}

func (s *messageSpool) effectiveTTL() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return spoolTTL
}

func (s *messageSpool) filterValid(items []spooledEnvelope, now time.Time) []spooledEnvelope {
	if len(items) == 0 {
		return nil
	}
	ttl := s.effectiveTTL()
	out := items[:0]
	for _, item := range items {
		if now.Sub(item.queuedAt) <= ttl {
			out = append(out, item)
		}
	}
	return out
}

func sealSpoolSnapshot(key [32]byte, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	return []byte(spoolCipherPrefix + base64.StdEncoding.EncodeToString(sealed)), nil
}

func openSpoolSnapshot(key [32]byte, raw []byte) ([]byte, error) {
	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, spoolCipherPrefix) {
		return raw, nil
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, spoolCipherPrefix))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < ns {
		return nil, io.ErrUnexpectedEOF
	}
	return gcm.Open(nil, blob[:ns], blob[ns:], nil)
}

// loadState 从 stateFile 加载持久化离线队列、公钥目录与频道结构快照（M18 / v8.1.0）。
func (s *messageSpool) loadState(now time.Time) []group.ChannelInfo {
	if s.stateFile == "" {
		return nil
	}
	raw, err := os.ReadFile(s.stateFile)
	if err != nil || len(raw) == 0 {
		return nil
	}
	plain, err := openSpoolSnapshot(s.encKey, raw)
	if err != nil || len(plain) == 0 {
		return nil
	}
	var st persistedServerState
	if err := json.Unmarshal(plain, &st); err != nil {
		return nil
	}
	ttl := s.effectiveTTL()
	for uid, items := range st.UserQueues {
		var q []spooledEnvelope
		for _, it := range items {
			if it.Env == nil {
				continue
			}
			ts := time.UnixMilli(it.QueuedAt)
			if now.Sub(ts) <= ttl {
				q = append(q, spooledEnvelope{env: cloneEnvelope(it.Env), queuedAt: ts})
			}
		}
		if len(q) > 0 {
			s.userQueues[uid] = q
		}
	}
	for cid, items := range st.ChannelHist {
		var q []spooledEnvelope
		for _, it := range items {
			if it.Env == nil {
				continue
			}
			ts := time.UnixMilli(it.QueuedAt)
			if now.Sub(ts) <= ttl {
				q = append(q, spooledEnvelope{env: cloneEnvelope(it.Env), queuedAt: ts})
			}
		}
		if len(q) > 0 {
			s.channelHist[cid] = q
		}
	}
	for uid, pub := range st.KeyDir {
		if uid != "" && pub != "" {
			s.keyDir[uid] = pub
		}
	}
	return st.Channels
}

// saveState 将当前离线队列、频道历史、公钥目录与活跃频道元信息经 AES-256-GCM 加密后原子落盘（M18 / v8.1.0）。
func (s *messageSpool) saveState(now time.Time) {
	if s.stateFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.stateFile), 0o755); err != nil {
		return
	}
	st := persistedServerState{
		UserQueues:  make(map[string][]persistedSpoolItem, len(s.userQueues)),
		ChannelHist: make(map[string][]persistedSpoolItem, len(s.channelHist)),
		KeyDir:      make(map[string]string, len(s.keyDir)),
	}
	for uid, q := range s.userQueues {
		for _, it := range s.filterValid(q, now) {
			st.UserQueues[uid] = append(st.UserQueues[uid], persistedSpoolItem{
				Env:      cloneEnvelope(it.env),
				QueuedAt: it.queuedAt.UnixMilli(),
			})
		}
	}
	for cid, q := range s.channelHist {
		for _, it := range s.filterValid(q, now) {
			st.ChannelHist[cid] = append(st.ChannelHist[cid], persistedSpoolItem{
				Env:      cloneEnvelope(it.env),
				QueuedAt: it.queuedAt.UnixMilli(),
			})
		}
	}
	for uid, pub := range s.keyDir {
		if uid != "" && pub != "" {
			st.KeyDir[uid] = pub
		}
	}
	if s.channelsFn != nil {
		st.Channels = s.channelsFn()
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	outBytes := raw
	if sealed, sErr := sealSpoolSnapshot(s.encKey, raw); sErr == nil {
		outBytes = sealed
	}
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, outBytes, 0o600); err == nil {
		_ = os.Rename(tmp, s.stateFile)
	}
}
