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
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m appapi.ChatMessage
		if err := json.Unmarshal([]byte(line), &m); err == nil && m.ID != "" {
			if !existing[m.ID] {
				existing[m.ID] = true
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
