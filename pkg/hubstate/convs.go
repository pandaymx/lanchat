package hubstate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// convLog 是会话注册表 logger。
var convLog = logging.New("hubstate.convs")

// LobbyConvID 是大厅（lobby）的会话 ID：空串表示大厅，与历史消息
// ConversationID 为空的语义一致（M12-A 之前所有消息都在大厅）。
const LobbyConvID = ""

// Convs 是会话注册表：回答「某个会话的成员是谁，消息该广播给谁」。
//
// 两种会话：
//   - 大厅（lobby）：隐式会话，成员 = 所有已握手用户（动态），不落库、
//     不保存成员表。任何用户天然在大厅，这是 M12-A 之前的默认行为。
//   - 群（group）：显式会话，创建时定初始成员、邀请可加人、退出可移除；
//     会话与成员都持久化（hub 重启后恢复）。
//
// 与 Registry 的关系：Convs 管「会话 → 用户集合」，Registry 管「用户 →
// 在线连接」。广播一条会话消息 = Convs.Members(convID) → 逐个用户
// Registry.PeersForUser → SendToPeers。
type Convs struct {
	mu sync.RWMutex
	// groups 是全部已加载的群：convID → 条目。大厅不在此表。
	groups map[string]*convGroup
}

// convGroup 是一个群的运行时条目。
type convGroup struct {
	conv    protocol.Conversation
	members map[string]struct{} // 成员 UserID 集合
}

// NewConvs 构造空会话注册表（不含任何群；大厅隐式存在）。
func NewConvs() *Convs {
	return &Convs{groups: make(map[string]*convGroup)}
}

// LoadFromStore 从 Store 恢复全部群与成员（hub 启动/重启调用）。
// store 为 nil 时静默跳过（纯内存 hub 无持久化）。
func (c *Convs) LoadFromStore(ctx context.Context, store interface {
	ListConversations(context.Context) ([]protocol.Conversation, error)
	ListConversationMembers(context.Context, string) ([]string, error)
},
) {
	if store == nil {
		return
	}
	convs, err := store.ListConversations(ctx)
	if err != nil {
		convLog.Warn("load conversations from store failed", "err", err)
		return
	}
	for _, cv := range convs {
		if cv.Kind != "group" || cv.ID == "" {
			continue // 大厅/未知类型不加载
		}
		members, err := store.ListConversationMembers(ctx, cv.ID)
		if err != nil {
			convLog.Warn("load members failed", "conv", cv.ID, "err", err)
			continue
		}
		c.mu.Lock()
		g := &convGroup{conv: cv, members: make(map[string]struct{}, len(members))}
		for _, u := range members {
			g.members[u] = struct{}{}
		}
		c.groups[cv.ID] = g
		c.mu.Unlock()
	}
	convLog.Info("conversations loaded", "count", len(c.groups))
}

// IsLobby 报告某会话是否是大厅（空串或旧客户端的 "lobby" 字符串）。
func (c *Convs) IsLobby(convID string) bool { return convID == LobbyConvID || convID == "lobby" }

// Get 返回会话元数据（不含成员）。大厅返回 Kind=="lobby" 的合成条目。
func (c *Convs) Get(convID string) (protocol.Conversation, bool) {
	if c.IsLobby(convID) {
		return protocol.Conversation{ID: "", Kind: "lobby", Title: "Lobby"}, true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	g, ok := c.groups[convID]
	if !ok {
		return protocol.Conversation{}, false
	}
	return g.conv, true
}

// Members 返回某会话的全部成员 UserID（大厅返回 nil——成员是隐式的，
// 调用方应按「所有在线用户」处理）。
func (c *Convs) Members(convID string) []string {
	if c.IsLobby(convID) {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	g, ok := c.groups[convID]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(g.members))
	for u := range g.members {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// IsMember 报告某用户是否在某会话中。大厅恒为 true（所有用户自动在）。
func (c *Convs) IsMember(convID, userID string) bool {
	if c.IsLobby(convID) {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	g, ok := c.groups[convID]
	if !ok {
		return false
	}
	_, ok = g.members[userID]
	return ok
}

// Snapshot 返回某会话的快照（含成员；大厅成员为 nil）。
func (c *Convs) Snapshot(convID string) (protocol.ConversationSnapshot, bool) {
	cv, ok := c.Get(convID)
	if !ok {
		return protocol.ConversationSnapshot{}, false
	}
	return protocol.ConversationSnapshot{Conversation: cv, Members: c.Members(convID)}, true
}

// ListSnapshots 返回全部群的快照（不含大厅；大厅由客户端隐式渲染）。
func (c *Convs) ListSnapshots() []protocol.ConversationSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]protocol.ConversationSnapshot, 0, len(c.groups))
	for _, g := range c.groups {
		members := make([]string, 0, len(g.members))
		for u := range g.members {
			members = append(members, u)
		}
		sort.Strings(members)
		out = append(out, protocol.ConversationSnapshot{Conversation: g.conv, Members: members})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Conversation.ID < out[j].Conversation.ID })
	return out
}

// convIDCounter 是 crypto/rand 失败时的退化计数器。
var convIDCounter uint64

// newConvID 生成 16 hex 字符的随机会话 ID（crypto/rand，防遍历）。
func newConvID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败基本不可恢复；退化为时间+计数器（低熵但可用）。
		return fmt.Sprintf("c%x%x", time.Now().UnixNano(), atomic.AddUint64(&convIDCounter, 1))
	}
	return hex.EncodeToString(b[:])
}

// Create 创建一个群并落库。memberIDs 为初始成员（不含创建者本人——
// 调用方把创建者也放进来的话会重复，这里幂等去重）。
// 返回会话元数据；store 为 nil 时仅内存。
func (c *Convs) Create(ctx context.Context, title string, memberIDs []string, store interface {
	SaveConversation(context.Context, protocol.Conversation) error
	SaveConversationMember(context.Context, string, string) error
},
) (protocol.Conversation, error) {
	conv := protocol.Conversation{ID: newConvID(), Kind: "group", Title: title}
	// 成员去重排序
	seen := make(map[string]struct{}, len(memberIDs))
	for _, u := range memberIDs {
		if u != "" {
			seen[u] = struct{}{}
		}
	}
	members := make([]string, 0, len(seen))
	for u := range seen {
		members = append(members, u)
	}
	sort.Strings(members)

	c.mu.Lock()
	g := &convGroup{conv: conv, members: make(map[string]struct{}, len(members))}
	for _, u := range members {
		g.members[u] = struct{}{}
	}
	c.groups[conv.ID] = g
	c.mu.Unlock()

	if store != nil {
		if err := store.SaveConversation(ctx, conv); err != nil {
			convLog.Warn("persist conversation failed", "conv", conv.ID, "err", err)
		}
		for _, u := range members {
			if err := store.SaveConversationMember(ctx, conv.ID, u); err != nil {
				convLog.Warn("persist member failed", "conv", conv.ID, "user", u, "err", err)
			}
		}
	}
	return conv, nil
}

// Invite 向群内添加成员（幂等）。返回新增的用户列表；会话不存在返回 false。
func (c *Convs) Invite(ctx context.Context, convID string, userIDs []string, store interface {
	SaveConversationMember(context.Context, string, string) error
},
) ([]string, bool) {
	if c.IsLobby(convID) {
		return nil, true // 大厅无需邀请（全员天然在）
	}
	c.mu.Lock()
	g, ok := c.groups[convID]
	if !ok {
		c.mu.Unlock()
		return nil, false
	}
	added := make([]string, 0, len(userIDs))
	for _, u := range userIDs {
		if u == "" {
			continue
		}
		if _, dup := g.members[u]; dup {
			continue
		}
		g.members[u] = struct{}{}
		added = append(added, u)
	}
	c.mu.Unlock()

	if store != nil {
		for _, u := range added {
			if err := store.SaveConversationMember(ctx, convID, u); err != nil {
				convLog.Warn("persist invite failed", "conv", convID, "user", u, "err", err)
			}
		}
	}
	sort.Strings(added)
	return added, true
}

// Leave 移除某用户出群（退群）。返回 false 表示会话不存在或用户本就不在。
func (c *Convs) Leave(ctx context.Context, convID, userID string, store interface {
	DeleteConversationMember(context.Context, string, string) error
},
) bool {
	if c.IsLobby(convID) {
		return false // 大厅不能退出
	}
	c.mu.Lock()
	g, ok := c.groups[convID]
	if !ok {
		c.mu.Unlock()
		return false
	}
	if _, in := g.members[userID]; !in {
		c.mu.Unlock()
		return false
	}
	delete(g.members, userID)
	c.mu.Unlock()

	if store != nil {
		if err := store.DeleteConversationMember(ctx, convID, userID); err != nil {
			convLog.Warn("persist leave failed", "conv", convID, "user", userID, "err", err)
		}
	}
	return true
}
