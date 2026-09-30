package group

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// 频道操作错误。
var (
	errChannelNotFound = errors.New("group: channel not found")
	errEmptyName       = errors.New("group: empty channel name")
	errNotOwner        = errors.New("group: not channel owner")
	errNotMember       = errors.New("group: not a channel member")
)

// channel 是单个 G2 频道的运行时状态。
type channel struct {
	id      string
	name    string
	ownerID string
	private bool
	members map[string]bool
}

// Registry 是 G2 自定义频道注册表：维护频道与成员关系，回答「一条群组
// 消息应扇出给谁」。它不持有传输/连接，供外层（信令中枢）接线调用；
// 不做持久化（方案 §9.6）。
type Registry struct {
	mu       sync.Mutex
	channels map[string]*channel
	sink     EventSink
}

// NewRegistry 构造空注册表。
func NewRegistry(sink EventSink) *Registry {
	return &Registry{channels: make(map[string]*channel), sink: sink}
}

// Create 创建频道：创建者成为 owner 与首个成员。private 控制是否需邀请
// 或 owner 审批才能加入。返回频道 ID。
func (r *Registry) Create(name, ownerID string, private bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errEmptyName
	}
	id := uuid.NewString()
	ch := &channel{
		id: id, name: name, ownerID: ownerID, private: private,
		members: map[string]bool{ownerID: true},
	}
	r.mu.Lock()
	r.channels[id] = ch
	r.mu.Unlock()
	r.emitChanged()
	return id, nil
}

// Join 加入频道。public 频道任意在线成员可加入；private 频道仅可经
// Invite 加入，直接加入会被拒绝。
func (r *Registry) Join(channelID, memberID string) error {
	r.mu.Lock()
	ch, ok := r.channels[channelID]
	if !ok {
		r.mu.Unlock()
		return errChannelNotFound
	}
	if ch.private && !ch.members[memberID] {
		r.mu.Unlock()
		return errNotMember
	}
	ch.members[memberID] = true
	r.mu.Unlock()
	r.emitChanged()
	return nil
}

// Invite 由 owner 邀请成员加入 private 频道（public 频道同样适用）。
func (r *Registry) Invite(channelID, ownerID, memberID string) error {
	r.mu.Lock()
	ch, ok := r.channels[channelID]
	if !ok {
		r.mu.Unlock()
		return errChannelNotFound
	}
	if ch.ownerID != ownerID {
		r.mu.Unlock()
		return errNotOwner
	}
	ch.members[memberID] = true
	r.mu.Unlock()
	r.emitChanged()
	return nil
}

// Leave 成员主动退出频道。owner 退出时，若仍有成员则把 owner 移交给
// 最早加入（ID 字典序最小）的成员；无人则销毁频道。
func (r *Registry) Leave(channelID, memberID string) error {
	r.mu.Lock()
	ch, ok := r.channels[channelID]
	if !ok {
		r.mu.Unlock()
		return errChannelNotFound
	}
	if !ch.members[memberID] {
		r.mu.Unlock()
		return errNotMember
	}
	delete(ch.members, memberID)
	remove := false
	if ch.ownerID == memberID {
		if next := smallestKey(ch.members); next != "" {
			ch.ownerID = next
		} else {
			remove = true
		}
	}
	if remove {
		delete(r.channels, channelID)
	}
	r.mu.Unlock()
	r.emitChanged()
	return nil
}

// Remove 用于外层在成员离线时清理其全部频道成员关系（不触发权限校验）。
// 返回受影响、仍存在的频道 ID 列表。
func (r *Registry) Remove(memberID string) []string {
	r.mu.Lock()
	affected := make(map[string]bool)
	for id, ch := range r.channels {
		if !ch.members[memberID] {
			continue
		}
		delete(ch.members, memberID)
		if ch.ownerID == memberID {
			if next := smallestKey(ch.members); next != "" {
				ch.ownerID = next
			} else {
				delete(r.channels, id)
				continue
			}
		}
		affected[id] = true
	}
	r.mu.Unlock()
	r.emitChanged()
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// IsMember 判断 memberID 是否在频道内。
func (r *Registry) IsMember(channelID, memberID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.channels[channelID]
	return ok && ch.members[memberID]
}

// Recipients 返回一条发往该频道的消息应扇出的成员 ID（除发送者外），
// 非成员发送返回错误——这是「消息仅成员可见」的强制点（方案 §9.6）。
func (r *Registry) Recipients(channelID, senderID string) ([]string, error) {
	r.mu.Lock()
	ch, ok := r.channels[channelID]
	if !ok {
		r.mu.Unlock()
		return nil, errChannelNotFound
	}
	if !ch.members[senderID] {
		r.mu.Unlock()
		return nil, errNotMember
	}
	out := make([]string, 0, len(ch.members))
	for id := range ch.members {
		if id != senderID {
			out = append(out, id)
		}
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out, nil
}

// List 返回当前全部频道的只读视图。
func (r *Registry) List() []ChannelInfo {
	r.mu.Lock()
	out := make([]ChannelInfo, 0, len(r.channels))
	for _, ch := range r.channels {
		members := make([]string, 0, len(ch.members))
		for id := range ch.members {
			members = append(members, id)
		}
		sort.Strings(members)
		out = append(out, ChannelInfo{
			ID: ch.id, Name: ch.name, OwnerID: ch.ownerID,
			Private: ch.private, Members: members,
		})
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListMember 返回某成员已加入的频道。
func (r *Registry) ListMember(memberID string) []ChannelInfo {
	all := r.List()
	var out []ChannelInfo
	for _, c := range all {
		for _, id := range c.Members {
			if id == memberID {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// emitChanged 上抛频道列表变化。
func (r *Registry) emitChanged() {
	if r.sink != nil {
		r.sink.OnChannel(ChannelEvent{Kind: ChannelChanged, Channels: r.List()})
	}
}

// smallestKey 返回 map 中字典序最小的键；空 map 返回 ""。
func smallestKey(m map[string]bool) string {
	var best string
	for k := range m {
		if best == "" || k < best {
			best = k
		}
	}
	return best
}
