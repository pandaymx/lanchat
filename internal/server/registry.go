package server

import (
	"time"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// registry 是在线表，持有 selfID → Client 的映射与单调递增的 revision。
// 仅由 hub 单个 goroutine 访问，本身不加锁。
type registry struct {
	revision uint64
	byID     map[string]*Client
}

func newRegistry() *registry {
	return &registry{byID: make(map[string]*Client)}
}

// add 加入客户端并令 revision+1，返回新 revision。
func (r *registry) add(c *Client) uint64 {
	r.byID[c.id] = c
	r.revision++
	return r.revision
}

// remove 删除客户端并令 revision+1；返回被删除的 client 与是否存在。
func (r *registry) remove(id string) (*Client, uint64, bool) {
	c, ok := r.byID[id]
	if !ok {
		return nil, r.revision, false
	}
	delete(r.byID, id)
	r.revision++
	return c, r.revision, true
}

// replace 仅做同 ID 替换，不改变 revision（单点登录，旧会话被新会话顶掉）。
func (r *registry) replace(c *Client) {
	r.byID[c.id] = c
}

func (r *registry) get(id string) (*Client, bool) {
	c, ok := r.byID[id]
	return c, ok
}

func (r *registry) len() int { return len(r.byID) }

// snapshot 返回当前 revision 与在线用户列表的拷贝。
func (r *registry) snapshot() (uint64, []protocol.User) {
	users := make([]protocol.User, 0, len(r.byID))
	for _, c := range r.byID {
		users = append(users, c.user())
	}
	return r.revision, users
}

// idleClients 返回 lastSeen 早于 cutoff 的所有客户端。
func (r *registry) idleClients(cutoff time.Time) []*Client {
	var out []*Client
	for _, c := range r.byID {
		if c.seenAt().Before(cutoff) {
			out = append(out, c)
		}
	}
	return out
}
