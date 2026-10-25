package server

import (
	"time"

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

// messageSpool 维护服务端内存级单播离线暂存队列（Store-and-Forward）与频道最近历史快照（M16）。
// 所有方法均在 hub 单 goroutine 内调用，无需额外互斥锁。
type messageSpool struct {
	userQueues  map[string][]spooledEnvelope
	channelHist map[string][]spooledEnvelope
}

func newMessageSpool() *messageSpool {
	return &messageSpool{
		userQueues:  make(map[string][]spooledEnvelope),
		channelHist: make(map[string][]spooledEnvelope),
	}
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
	return true
}

// drainUser 取出并清空目标用户的所有未过期离线暂存消息。
func (s *messageSpool) drainUser(userID string, now time.Time) []*protocol.Envelope {
	q, ok := s.userQueues[userID]
	if !ok || len(q) == 0 {
		return nil
	}
	delete(s.userQueues, userID)
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
