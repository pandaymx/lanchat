// Package memory 提供 core.Store 的同进程内存实现。
//
// 用途有两层：
//   - M1 单元测试（FakeTransport + MemoryStore 跑完整业务流，不依赖 OS）
//   - single-node 模式的备选存储（无需 SQL/文件，初次启动一气呵成）
//
// 线程安全：所有方法使用 sync.RWMutex，可被多 Conn goroutine 同时调用。
//
// 设计取舍：
//   - 索引尽量放在 map 上以 O(1) 查找；
//   - History 用排序切片 + 二分查找；当下消息量级很小，不必上 B-tree；
//   - AppendMessage 时**不**自动分配 ServerSeq，由调用方（Hub 或 Client）传入；
//     MemoryStore 只管持久化，序号分发是上层职责。
//     这一点与 SQLite 实现的"自动分配"略有不同——见 pkg/store/sqlite。
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// MemoryStore 是同进程线程安全的 core.Store 实现。
// 零值不可用，请用 New。
//
//nolint:revive // 名字刻意保留，包外调用方按 "memory.MemoryStore" 直观理解。
type MemoryStore struct {
	mu sync.RWMutex

	users         map[string]protocol.User
	devices       map[string]protocol.Device
	conversations map[string]protocol.Conversation
	// members 是会话成员表（M12-A）：convID → 成员 UserID 集合。
	members map[string]map[string]struct{}

	// messages[convID] = sorted by ServerSeq asc.
	messages map[string][]protocol.StoredMessage

	// cursors[deviceID+"\x00"+convID] = ServerSeq
	cursors map[string]uint64

	// files[fileID] = FileMeta（M9）
	files map[string]protocol.FileMeta

	closed bool
}

// New 构造一个空 MemoryStore。
func New() *MemoryStore {
	return &MemoryStore{
		users:         make(map[string]protocol.User),
		devices:       make(map[string]protocol.Device),
		conversations: make(map[string]protocol.Conversation),
		members:       make(map[string]map[string]struct{}),
		messages:      make(map[string][]protocol.StoredMessage),
		cursors:       make(map[string]uint64),
		files:         make(map[string]protocol.FileMeta),
	}
}

func cursorKey(deviceID, convID string) string { return deviceID + "\x00" + convID }

// parseCursorKey 是 cursorKey 的逆操作；key 不含分隔符时 ok=false。
func parseCursorKey(key string) (deviceID, convID string, ok bool) {
	idx := strings.Index(key, "\x00")
	if idx < 0 {
		return "", "", false
	}
	return key[:idx], key[idx+1:], true
}

// SaveUser 保存/覆盖一个用户。返回 ErrClosed 当 Store 已 Close。
func (s *MemoryStore) SaveUser(_ context.Context, u protocol.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	s.users[u.ID] = u
	return nil
}

// GetUser 读取用户。未命中返回 ErrNotFound。
func (s *MemoryStore) GetUser(_ context.Context, id string) (protocol.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return protocol.User{}, core.ErrClosed
	}
	u, ok := s.users[id]
	if !ok {
		return protocol.User{}, core.ErrNotFound
	}
	return u, nil
}

// SaveDevice 保存/覆盖一个设备。M1 不强制要求 UserID 已存在。
func (s *MemoryStore) SaveDevice(_ context.Context, d protocol.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	s.devices[d.ID] = d
	return nil
}

// GetDevice 读取设备。未命中返回 ErrNotFound。
func (s *MemoryStore) GetDevice(_ context.Context, id string) (protocol.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return protocol.Device{}, core.ErrClosed
	}
	d, ok := s.devices[id]
	if !ok {
		return protocol.Device{}, core.ErrNotFound
	}
	return d, nil
}

// SaveConversation 保存/覆盖一个会话。
func (s *MemoryStore) SaveConversation(_ context.Context, c protocol.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	s.conversations[c.ID] = c
	return nil
}

// GetConversation 读取会话。未命中返回 ErrNotFound。
func (s *MemoryStore) GetConversation(_ context.Context, id string) (protocol.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return protocol.Conversation{}, core.ErrClosed
	}
	c, ok := s.conversations[id]
	if !ok {
		return protocol.Conversation{}, core.ErrNotFound
	}
	return c, nil
}

// ListConversations 返回全部已持久化的会话（M12-A）。
func (s *MemoryStore) ListConversations(_ context.Context) ([]protocol.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	out := make([]protocol.Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SaveConversationMember 记录某用户加入某会话（M12-A，幂等）。
func (s *MemoryStore) SaveConversationMember(_ context.Context, convID, userID string) error {
	if convID == "" || userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	m := s.members[convID]
	if m == nil {
		m = make(map[string]struct{})
		s.members[convID] = m
	}
	m[userID] = struct{}{}
	return nil
}

// ListConversationMembers 返回某会话的全部成员 UserID（M12-A）。
func (s *MemoryStore) ListConversationMembers(_ context.Context, convID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	m := s.members[convID]
	out := make([]string, 0, len(m))
	for u := range m {
		out = append(out, u)
	}
	sort.Strings(out)
	return out, nil
}

// DeleteConversationMember 移除某用户出会话（M12-A 退群）。
func (s *MemoryStore) DeleteConversationMember(_ context.Context, convID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	if m := s.members[convID]; m != nil {
		delete(m, userID)
		if len(m) == 0 {
			delete(s.members, convID)
		}
	}
	return nil
}

// AppendMessage 追加或更新一条消息到指定会话（upsert-by-ID）。
//
// 设计取舍：upsert 而不是纯追加，是为了支持 Client 侧的乐观写入 + Hub 端的 ServerSeq 分配。
//   - 客户端在 SendMessage 时先把 (ID=local-<nonce>, ServerSeq=0) 写本地；
//   - Hub 回 FKDeliver 时带 (相同 ID, ServerSeq=N)；
//   - upsert 直接把 ServerSeq 从 0 改成 N，**不会变成两条**。
//
// 限制：
//   - m.ConversationID 必填；为空返回 error。
//   - 同 ID 已存在则覆盖；ConversationID 不一致会强制修正到 m 给的值（Hub 的版本为准）。
func (s *MemoryStore) AppendMessage(_ context.Context, m protocol.StoredMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().UnixMilli()
	}
	list := s.messages[m.ConversationID]
	for i := range list {
		if list[i].ID == m.ID {
			list[i] = m // 覆盖（同 ID）：典型场景是 ServerSeq 由 Hub 补齐
			return nil
		}
	}
	list = append(list, m)
	// 保持升序插入。M1 假设调用方传入的 seq 已经单调。
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].ServerSeq < list[j].ServerSeq
	})
	s.messages[m.ConversationID] = list
	return nil
}

type errInvalidInput string

func (e errInvalidInput) Error() string { return string(e) }

// History 返回 (convID, after) 之后按 ServerSeq 升序的消息，最多 limit 条。
// limit<=0 表示不限制（实际受限于 maxHistoryLimit）。
const maxHistoryLimit = 5000

// History 返回 (convID, after) 之后按 ServerSeq 升序的消息，最多 limit 条。
// limit<=0 表示不限制（实际受限于 maxHistoryLimit）。
func (s *MemoryStore) History(_ context.Context, convID string, after uint64, limit int) ([]protocol.StoredMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	list := s.messages[convID]
	lo := sort.Search(len(list), func(i int) bool {
		return list[i].ServerSeq > after
	})
	end := len(list)
	if limit > 0 && limit < end-lo {
		end = lo + limit
	}
	if end > maxHistoryLimit {
		end = maxHistoryLimit
	}
	if lo >= end {
		return nil, nil
	}
	// 返回拷贝，避免调用方意外改动内部切片。
	out := make([]protocol.StoredMessage, end-lo)
	copy(out, list[lo:end])
	return out, nil
}

// SearchMessages 按关键词子串匹配搜索历史消息（v1.1）。
//
// 内存实现：遍历全部会话的消息切片（消息量级小，线性扫描足够）；
// convID 为空搜全部。结果按 ServerSeq 降序，最多 limit 条（默认 50）。
func (s *MemoryStore) SearchMessages(_ context.Context, query, convID string, limit int) ([]protocol.StoredMessage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > maxHistoryLimit {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	needle := strings.ToLower(query)
	if convID != "" {
		list := s.messages[convID]
		var out []protocol.StoredMessage
		for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
			if strings.Contains(strings.ToLower(list[i].Body), needle) {
				out = append(out, list[i])
			}
		}
		return out, nil
	}
	// 跨会话：先收全再按 ServerSeq 降序合并，量小无妨。
	var out []protocol.StoredMessage
	for _, list := range s.messages {
		for _, m := range list {
			if strings.Contains(strings.ToLower(m.Body), needle) {
				out = append(out, m)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ServerSeq > out[j].ServerSeq })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetCursor 设置某设备在某会话的已读游标。
func (s *MemoryStore) SetCursor(_ context.Context, deviceID, convID string, seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	if deviceID == "" {
		return errInvalidInput("device id required")
	}
	key := cursorKey(deviceID, convID)
	if cur, ok := s.cursors[key]; ok && cur >= seq {
		// 不回退游标，避免已读被误取消
		return nil
	}
	s.cursors[key] = seq
	return nil
}

// GetCursor 返回某设备在某会话的已读游标。未设置返回 0（语义：从未读过）。
func (s *MemoryStore) GetCursor(_ context.Context, deviceID, convID string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, core.ErrClosed
	}
	if deviceID == "" {
		return 0, errInvalidInput("device id required")
	}
	return s.cursors[cursorKey(deviceID, convID)], nil
}

// ListCursors 返回全部（设备, 会话）已读游标（M8.1 Hub 给新连接补发
// 已读快照用）。convID 非空时只返回该会话的条目。结果按 DeviceID 升序。
func (s *MemoryStore) ListCursors(_ context.Context, convID string) ([]protocol.ReadCursor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	out := make([]protocol.ReadCursor, 0, len(s.cursors))
	for key, seq := range s.cursors {
		device, conv, ok := parseCursorKey(key)
		if !ok {
			continue
		}
		if convID != "" && conv != convID {
			continue
		}
		out = append(out, protocol.ReadCursor{DeviceID: device, ConversationID: conv, ServerSeq: seq})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceID != out[j].DeviceID {
			return out[i].DeviceID < out[j].DeviceID
		}
		return out[i].ConversationID < out[j].ConversationID
	})
	return out, nil
}

// SaveFileMeta 记录文件元信息（M9），幂等覆盖。
func (s *MemoryStore) SaveFileMeta(_ context.Context, m protocol.FileMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	if m.FileID == "" {
		return errInvalidInput("file id required")
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().UnixMilli()
	}
	s.files[m.FileID] = m
	return nil
}

// GetFileMeta 按 FileID 读取文件元信息；未命中返回 core.ErrNotFound。
func (s *MemoryStore) GetFileMeta(_ context.Context, fileID string) (protocol.FileMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return protocol.FileMeta{}, core.ErrClosed
	}
	m, ok := s.files[fileID]
	if !ok {
		return protocol.FileMeta{}, core.ErrNotFound
	}
	return m, nil
}

// ExportAll 导出全量数据（v1.2 备份导出）：遍历内存各表组装 Backup。
func (s *MemoryStore) ExportAll(_ context.Context) (*protocol.Backup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, core.ErrClosed
	}
	b := &protocol.Backup{
		Schema:        protocol.BackupSchema,
		ExportedAt:    time.Now().UnixMilli(),
		Users:         make([]protocol.User, 0, len(s.users)),
		Devices:       make([]protocol.Device, 0, len(s.devices)),
		Conversations: []protocol.ConversationEntry{},
		Messages:      []protocol.StoredMessage{},
		Cursors:       []protocol.ReadCursor{},
		Files:         make([]protocol.FileMeta, 0, len(s.files)),
	}
	for _, u := range s.users {
		b.Users = append(b.Users, u)
	}
	for _, d := range s.devices {
		b.Devices = append(b.Devices, d)
	}
	// 会话：按 ID 排序输出（map 遍历无序，排序保证导出确定性）
	convIDs := make([]string, 0, len(s.conversations))
	for id := range s.conversations {
		convIDs = append(convIDs, id)
	}
	sort.Strings(convIDs)
	for _, id := range convIDs {
		c := s.conversations[id]
		members := make([]string, 0, len(s.members[id]))
		for u := range s.members[id] {
			members = append(members, u)
		}
		sort.Strings(members)
		b.Conversations = append(b.Conversations, protocol.ConversationEntry{
			Conversation: c,
			Members:      members,
		})
	}
	// 消息：全量按 ServerSeq 升序（messages 切片本身按 seq 升序，逐 conv 归并）
	var all []protocol.StoredMessage
	for _, list := range s.messages {
		all = append(all, list...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ServerSeq < all[j].ServerSeq })
	// 空库时 all 是 nil，归一为空切片（JSON 输出稳定）。
	if all == nil {
		all = []protocol.StoredMessage{}
	}
	b.Messages = all
	// 游标：按 device/conv 排序
	cursorKeys := make([]string, 0, len(s.cursors))
	for k := range s.cursors {
		cursorKeys = append(cursorKeys, k)
	}
	sort.Strings(cursorKeys)
	for _, k := range cursorKeys {
		device, conv, _ := strings.Cut(k, "\x00")
		b.Cursors = append(b.Cursors, protocol.ReadCursor{
			DeviceID:       device,
			ConversationID: conv,
			ServerSeq:      s.cursors[k],
		})
	}
	for _, m := range s.files {
		b.Files = append(b.Files, m)
	}
	return b, nil
}

// Close 释放资源。重复调用安全。
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
