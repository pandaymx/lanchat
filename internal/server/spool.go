package server

import (
	"encoding/json"
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
	// maxChannelHistory 是单频道保留的最近历史消息快照条数上限（M16）。
	maxChannelHistory = 50
	// spoolTTL 是离线暂存与频道历史消息的最大存活时长（24 小时）。
	spoolTTL = 24 * time.Hour
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
}

// messageSpool 维护服务端单播离线暂存队列（Store-and-Forward）、频道最近历史快照（M16）
// 以及可选的原子落盘快照持久化（M18）。
// 所有方法均在 hub 单 goroutine 内调用，无需额外互斥锁。
type messageSpool struct {
	stateFile   string
	userQueues  map[string][]spooledEnvelope
	channelHist map[string][]spooledEnvelope
	channelsFn  func() []group.ChannelInfo
}

func newMessageSpool(stateFile string, channelsFn func() []group.ChannelInfo) (*messageSpool, []group.ChannelInfo) {
	s := &messageSpool{
		stateFile:   strings.TrimSpace(stateFile),
		userQueues:  make(map[string][]spooledEnvelope),
		channelHist: make(map[string][]spooledEnvelope),
		channelsFn:  channelsFn,
	}
	restored := s.loadState(time.Now())
	return s, restored
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

// enqueueUser 将单播消息追加到目标用户的离线环形队列（上限 100 条，TTL 24h）。
func (s *messageSpool) enqueueUser(userID string, env *protocol.Envelope, now time.Time) bool {
	if !canQueueTarget(userID) || !isSpoolableType(env.Type) {
		return false
	}
	q := s.filterValid(s.userQueues[userID], now)
	q = append(q, spooledEnvelope{
		env:      cloneEnvelope(env),
		queuedAt: now,
	})
	if len(q) > maxSpoolPerUser {
		q = append([]spooledEnvelope(nil), q[len(q)-maxSpoolPerUser:]...)
	}
	s.userQueues[userID] = q
	s.saveState(now)
	return true
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

// prune 清理所有已超过 TTL 的离线暂存与频道历史消息。
func (s *messageSpool) prune(now time.Time) {
	for uid, q := range s.userQueues {
		valid := s.filterValid(q, now)
		if len(valid) == 0 {
			delete(s.userQueues, uid)
		} else {
			s.userQueues[uid] = valid
		}
	}
	for cid, q := range s.channelHist {
		valid := s.filterValid(q, now)
		if len(valid) == 0 {
			delete(s.channelHist, cid)
		} else {
			s.channelHist[cid] = valid
		}
	}
}

func (s *messageSpool) filterValid(items []spooledEnvelope, now time.Time) []spooledEnvelope {
	if len(items) == 0 {
		return nil
	}
	out := items[:0]
	for _, item := range items {
		if now.Sub(item.queuedAt) <= spoolTTL {
			out = append(out, item)
		}
	}
	return out
}

// loadState 从 stateFile 加载持久化离线队列与频道结构快照（M18）。
func (s *messageSpool) loadState(now time.Time) []group.ChannelInfo {
	if s.stateFile == "" {
		return nil
	}
	raw, err := os.ReadFile(s.stateFile)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var st persistedServerState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil
	}
	for uid, items := range st.UserQueues {
		var q []spooledEnvelope
		for _, it := range items {
			if it.Env == nil {
				continue
			}
			ts := time.UnixMilli(it.QueuedAt)
			if now.Sub(ts) <= spoolTTL {
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
			if now.Sub(ts) <= spoolTTL {
				q = append(q, spooledEnvelope{env: cloneEnvelope(it.Env), queuedAt: ts})
			}
		}
		if len(q) > 0 {
			s.channelHist[cid] = q
		}
	}
	return st.Channels
}

// saveState 将当前离线队列、频道历史与活跃频道元信息原子落盘（M18）。
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
	if s.channelsFn != nil {
		st.Channels = s.channelsFn()
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		_ = os.Rename(tmp, s.stateFile)
	}
}
