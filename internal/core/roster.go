package core

import (
	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// toPeer 协议 User → appapi.Peer。
func toPeer(u protocol.User) appapi.Peer {
	return appapi.Peer{ID: u.ID, Nickname: u.Nickname, OS: u.OS, Status: u.Status}
}

// applyUserList 用全量在线表替换 peers；重连后 UI 依赖 join 事件重建视图，
// 因此对新增用户逐一发 OnPeerJoined，对消失用户发 OnPeerLeft。
func (c *Client) applyUserList(env *protocol.Envelope) {
	var p protocol.UserListPayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}

	c.mu.Lock()
	selfID := c.selfID
	old := c.peers
	next := make(map[string]appapi.Peer, len(p.Users))
	var joined []appapi.Peer
	var left []string
	for _, u := range p.Users {
		if u.ID == selfID {
			continue
		}
		peer := toPeer(u)
		next[u.ID] = peer
		if _, existed := old[u.ID]; !existed {
			joined = append(joined, peer)
		}
	}
	for id := range old {
		if _, keep := next[id]; !keep {
			left = append(left, id)
		}
	}
	c.peers = next
	c.mu.Unlock()

	l := c.listener()
	for _, peer := range joined {
		l.OnPeerJoined(peer)
	}
	for _, id := range left {
		l.OnPeerLeft(id)
	}
}

// applyJoin 增量上线。
func (c *Client) applyJoin(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if p.User.ID == c.selfID {
		c.mu.Unlock()
		return
	}
	c.peers[p.User.ID] = toPeer(p.User)
	peer := c.peers[p.User.ID]
	c.mu.Unlock()
	c.listener().OnPeerJoined(peer)
}

// applyLeave 增量下线。
func (c *Client) applyLeave(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if _, ok := c.peers[p.User.ID]; !ok {
		c.mu.Unlock()
		return
	}
	delete(c.peers, p.User.ID)
	c.mu.Unlock()
	c.listener().OnPeerLeft(p.User.ID)
}

// applyPresence 更新资料；UI 经 OnPeerJoined 复用刷新视图。
func (c *Client) applyPresence(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if p.User.ID == c.selfID {
		c.mu.Unlock()
		return
	}
	c.peers[p.User.ID] = toPeer(p.User)
	peer := c.peers[p.User.ID]
	c.mu.Unlock()
	c.listener().OnPeerJoined(peer)
}
