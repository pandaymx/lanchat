package server

import (
	"github.com/pandaymx/lanchat/internal/protocol"
)

// groupSession 是一次群组文件分发（GROUP_OFFER）在服务端的轻量会话状态。
//
// 服务端对 swarm 块交换无感，只按 groupID+transferID 记录源与已加入成员，
// 负责转发 OFFER/JOIN/LEAVE/PROGRESS，并在成员离线时清理。成员的块端口
// 候选与令牌原样透传，供各端建立 P2P 块连接。
type groupSession struct {
	groupID    string
	transferID string
	source     string

	// members 为已加入成员（不含 source）。
	members map[string]bool
	// cand/token 记录各成员随 GROUP_JOIN 上报的块端口候选与令牌。
	cand  map[string][]string
	token map[string]string
}

// swarmSessions 管理全部进行中的群组文件分发会话。
// 仅在 hub 单 goroutine 内访问，无需加锁。
type swarmSessions struct {
	sessions map[string]*groupSession // key: sessionKey(groupID, transferID)
}

func newSwarmSessions() *swarmSessions {
	return &swarmSessions{sessions: map[string]*groupSession{}}
}

func sessionKey(groupID, transferID string) string { return groupID + "\x00" + transferID }

func (s *swarmSessions) get(groupID, transferID string) (*groupSession, bool) {
	g, ok := s.sessions[sessionKey(groupID, transferID)]
	return g, ok
}

// create 登记一次新分发的源；若会话已存在则返回 false。
func (s *swarmSessions) create(groupID, transferID, source string) (*groupSession, bool) {
	key := sessionKey(groupID, transferID)
	if _, ok := s.sessions[key]; ok {
		return nil, false
	}
	g := &groupSession{
		groupID:    groupID,
		transferID: transferID,
		source:     source,
		members:    map[string]bool{},
		cand:       map[string][]string{},
		token:      map[string]string{},
	}
	s.sessions[key] = g
	return g, true
}

// join 把成员加入会话；新成员返回 true。
func (g *groupSession) join(id string, cand []string, token string) bool {
	if g.members[id] {
		return false
	}
	g.members[id] = true
	g.cand[id] = cand
	g.token[id] = token
	return true
}

// leave 移除成员；成员存在返回 true。
func (g *groupSession) leave(id string) bool {
	if !g.members[id] {
		return false
	}
	delete(g.members, id)
	delete(g.cand, id)
	delete(g.token, id)
	return true
}

// removeClientAll 清理离线成员涉及的全部会话。返回：
//   - ended：离线者是源、已被整体终止的会话（需通知原成员该分发结束）；
//   - left：离线者是普通成员、仅移除该成员的会话（需向源及其他成员转发 LEAVE）。
func (s *swarmSessions) removeClientAll(id string) (ended, left []*groupSession) {
	for key, g := range s.sessions {
		if g.source == id {
			delete(s.sessions, key)
			ended = append(ended, g)
			continue
		}
		if g.members[id] {
			g.leave(id)
			left = append(left, g)
		}
	}
	return ended, left
}

// relayTo 把信封单播给 target，不回 ACK（服务端转发路径）。
func (h *hub) relayTo(target string, env *protocol.Envelope) {
	c, ok := h.reg.get(target)
	if !ok {
		return
	}
	if !c.enqueue(env) {
		h.drop(c)
	}
}
