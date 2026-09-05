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

	// messages[convID] = sorted by ServerSeq asc.
	messages map[string][]protocol.StoredMessage

	// cursors[deviceID+"\x00"+convID] = ServerSeq
	cursors map[string]uint64

	closed bool
}

// New 构造一个空 MemoryStore。
func New() *MemoryStore {
	return &MemoryStore{
		users:         make(map[string]protocol.User),
		devices:       make(map[string]protocol.Device),
		conversations: make(map[string]protocol.Conversation),
		messages:      make(map[string][]protocol.StoredMessage),
		cursors:       make(map[string]uint64),
	}
}

func cursorKey(deviceID, convID string) string { return deviceID + "\x00" + convID }

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
	if m.ConversationID == "" {
		return errMissingConv
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

var errMissingConv = errInvalidInput("conversation id required")

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

// SetCursor 设置某设备在某会话的已读游标。
func (s *MemoryStore) SetCursor(_ context.Context, deviceID, convID string, seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrClosed
	}
	if deviceID == "" || convID == "" {
		return errInvalidInput("device id and conv id required")
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
	if deviceID == "" || convID == "" {
		return 0, errInvalidInput("device id and conv id required")
	}
	return s.cursors[cursorKey(deviceID, convID)], nil
}

// Close 释放资源。重复调用安全。
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
