package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pandaymx/lanchat/internal/appapi"
)

const (
	maxHistoryMessages = 500
	historyFileName    = "lanchat-history.jsonl"
)

// historyStore 维护内存环形缓冲与可选的本地 JSONL 聊天历史文件（零 CGO）。
type historyStore struct {
	mu   sync.Mutex
	dir  string
	msgs []appapi.ChatMessage
}

func newHistoryStore(dir string) *historyStore {
	h := &historyStore{}
	if dir != "" {
		h.setDir(dir)
	}
	return h
}

func (h *historyStore) setDir(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dir = dir
	if dir == "" {
		return
	}
	f, err := os.Open(filepath.Join(dir, historyFileName))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	existing := make(map[string]bool, len(h.msgs))
	for _, m := range h.msgs {
		if m.ID != "" {
			existing[m.ID] = true
		}
	}

	var loaded []appapi.ChatMessage
	idxByID := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m appapi.ChatMessage
		if err := json.Unmarshal([]byte(line), &m); err == nil && m.ID != "" {
			if idx, ok := idxByID[m.ID]; ok {
				loaded[idx] = m
			} else if !existing[m.ID] {
				idxByID[m.ID] = len(loaded)
				loaded = append(loaded, m)
			}
		}
	}
	combined := append(loaded, h.msgs...)
	if len(combined) > maxHistoryMessages {
		combined = combined[len(combined)-maxHistoryMessages:]
	}
	h.msgs = combined
}

func (h *historyStore) append(m appapi.ChatMessage) {
	if m.TS == 0 {
		m.TS = time.Now().UnixMilli()
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, existing := range h.msgs {
		if m.ID != "" && existing.ID == m.ID {
			return
		}
	}
	h.msgs = append(h.msgs, m)
	if len(h.msgs) > maxHistoryMessages {
		h.msgs = append([]appapi.ChatMessage(nil), h.msgs[len(h.msgs)-maxHistoryMessages:]...)
	}
	h.persistLocked(m)
}

func (h *historyStore) persistLocked(m appapi.ChatMessage) {
	if h.dir == "" {
		return
	}
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(h.dir, historyFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if raw, err := json.Marshal(m); err == nil {
		_, _ = f.Write(append(raw, '\n'))
	}
}

// revoke 撤回指定消息（校验发送者与最大可撤回时限）。
func (h *historyStore) revoke(msgID, actorID string, maxAge time.Duration) (appapi.ChatMessage, bool) {
	if msgID == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.msgs {
		if h.msgs[i].ID != msgID {
			continue
		}
		if actorID != "" && h.msgs[i].From != "" && h.msgs[i].From != actorID {
			return appapi.ChatMessage{}, false
		}
		if maxAge > 0 && h.msgs[i].TS > 0 {
			if time.Since(time.UnixMilli(h.msgs[i].TS)) > maxAge {
				return appapi.ChatMessage{}, false
			}
		}
		h.msgs[i].Revoked = true
		h.msgs[i].Text = "[消息已撤回]"
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// markRead 记录指定消息的已读回执（幂等去重）。
func (h *historyStore) markRead(msgID, readerID string) (appapi.ChatMessage, bool) {
	if msgID == "" || readerID == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.msgs {
		if h.msgs[i].ID != msgID {
			continue
		}
		for _, r := range h.msgs[i].ReadBy {
			if r == readerID {
				return h.msgs[i], true
			}
		}
		h.msgs[i].ReadBy = append(h.msgs[i].ReadBy, readerID)
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// search 在本地历史中按关键字（大小写不敏感）与可选会话 ID 检索未撤回消息。
func (h *historyStore) search(query, convID string) []appapi.ChatMessage {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []appapi.ChatMessage
	for _, m := range h.msgs {
		if m.Revoked {
			continue
		}
		if convID != "" && m.Group != convID && m.From != convID && m.To != convID {
			continue
		}
		if strings.Contains(strings.ToLower(m.Text), q) {
			out = append(out, m)
		}
	}
	return out
}

func (h *historyStore) snapshot() []appapi.ChatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.msgs) == 0 {
		return nil
	}
	out := make([]appapi.ChatMessage, len(h.msgs))
	copy(out, h.msgs)
	return out
}

// extractMentionsAndReply 从消息文本中解析可选的 `[reply:<msgID>]` 前缀与 `@mention` 标记。
func extractMentionsAndReply(text string) (replyTo string, mentions []string) {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"> [reply:", "[reply:"} {
		if strings.HasPrefix(trimmed, prefix) {
			rest := trimmed[len(prefix):]
			if idx := strings.IndexByte(rest, ']'); idx > 0 {
				replyTo = strings.TrimSpace(rest[:idx])
				break
			}
		}
	}

	seen := map[string]bool{}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' {
			continue
		}
		if i > 0 && !unicode.IsSpace(runes[i-1]) {
			continue
		}
		j := i + 1
		for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_' || runes[j] == '-') {
			j++
		}
		if j > i+1 {
			token := string(runes[i+1 : j])
			if !seen[token] {
				seen[token] = true
				mentions = append(mentions, token)
			}
			i = j - 1
		}
	}
	return replyTo, mentions
}
