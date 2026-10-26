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
	"github.com/pandaymx/lanchat/internal/transfer"
)

const (
	maxHistoryMessages = 500
	historyFileName    = "lanchat-history.jsonl"
)

// historyStore 维护内存环形缓冲与可选的本地 JSONL 聊天历史文件（零 CGO，M23 支持 AES-GCM 静态加密落盘）。
type historyStore struct {
	mu        sync.Mutex
	dir       string
	atRestKey string
	msgs      []appapi.ChatMessage
	rawCipher map[string]string
}

func newHistoryStore(dir, atRestKey string) *historyStore {
	h := &historyStore{
		atRestKey: atRestKey,
		rawCipher: make(map[string]string),
	}
	if dir != "" {
		h.setDir(dir, atRestKey)
	}
	return h
}

func (h *historyStore) setDir(dir, atRestKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dir = dir
	if atRestKey != "" {
		h.atRestKey = atRestKey
	}
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
		rawBytes, openErr := transfer.OpenHistoryLine(h.atRestKey, line)
		if openErr != nil {
			continue
		}
		var m appapi.ChatMessage
		if err := json.Unmarshal(rawBytes, &m); err == nil && m.ID != "" {
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

func (h *historyStore) append(m appapi.ChatMessage) bool {
	if m.TS == 0 {
		m.TS = time.Now().UnixMilli()
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, existing := range h.msgs {
		if m.ID != "" && existing.ID == m.ID {
			return false
		}
	}
	h.msgs = append(h.msgs, m)
	if len(h.msgs) > maxHistoryMessages {
		h.msgs = append([]appapi.ChatMessage(nil), h.msgs[len(h.msgs)-maxHistoryMessages:]...)
	}
	h.persistLocked(m)
	return true
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
		line := string(raw)
		if h.atRestKey != "" {
			if sealed, sErr := transfer.SealHistoryLine(h.atRestKey, raw); sErr == nil {
				line = sealed
			}
		}
		_, _ = f.WriteString(line + "\n")
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

// storePendingCipher 记录尚未收到频道密钥时的原始密文，供密钥到达后回溯解密（M19）。
func (h *historyStore) storePendingCipher(msgID, wire string) {
	if msgID == "" || wire == "" {
		return
	}
	h.mu.Lock()
	if h.rawCipher == nil {
		h.rawCipher = make(map[string]string)
	}
	h.rawCipher[msgID] = wire
	h.mu.Unlock()
}

// decryptChannelPending 在收到频道对称密钥后，回溯解密该频道已入库的历史加密消息（M19）。
func (h *historyStore) decryptChannelPending(channelID, chKey string) []appapi.ChatMessage {
	if channelID == "" || chKey == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var updated []appapi.ChatMessage
	for i := range h.msgs {
		if h.msgs[i].Group != channelID {
			continue
		}
		wire := h.rawCipher[h.msgs[i].ID]
		if wire == "" && strings.HasPrefix(h.msgs[i].Text, transfer.TextChannelPrefix) {
			wire = h.msgs[i].Text
		}
		if wire == "" {
			continue
		}
		if plain, err := transfer.OpenChannelText(chKey, wire); err == nil {
			h.msgs[i].Text = plain
			delete(h.rawCipher, h.msgs[i].ID)
			cp := h.msgs[i]
			h.persistLocked(cp)
			updated = append(updated, cp)
		}
	}
	return updated
}

// react 为指定消息添加表情回应并持久化（M20）。
func (h *historyStore) react(msgID, emoji string) (appapi.ChatMessage, bool) {
	emoji = strings.TrimSpace(emoji)
	if msgID == "" || emoji == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	badge := "[" + emoji + "]"
	for i := range h.msgs {
		if h.msgs[i].ID != msgID || h.msgs[i].Revoked {
			continue
		}
		if !strings.Contains(h.msgs[i].Text, badge) {
			h.msgs[i].Text = h.msgs[i].Text + " " + badge
		}
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// edit 原地修改指定消息文本（校验发送者，保留置顶前缀，M20）。
func (h *historyStore) edit(msgID, actorID, newText string) (appapi.ChatMessage, bool) {
	newText = strings.TrimSpace(newText)
	if msgID == "" || newText == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.msgs {
		if h.msgs[i].ID != msgID || h.msgs[i].Revoked {
			continue
		}
		if actorID != "" && h.msgs[i].From != "" && h.msgs[i].From != actorID {
			return appapi.ChatMessage{}, false
		}
		prefix := ""
		if strings.HasPrefix(h.msgs[i].Text, "📌 [置顶] ") {
			prefix = "📌 [置顶] "
		}
		h.msgs[i].Text = prefix + newText + " (已编辑)"
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// pin 将指定消息标记为置顶公告并持久化（M20）。
func (h *historyStore) pin(msgID string) (appapi.ChatMessage, bool) {
	if msgID == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.msgs {
		if h.msgs[i].ID != msgID || h.msgs[i].Revoked {
			continue
		}
		if !strings.HasPrefix(h.msgs[i].Text, "📌 [置顶] ") {
			h.msgs[i].Text = "📌 [置顶] " + h.msgs[i].Text
		}
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

func (h *historyStore) findByID(msgID string) (appapi.ChatMessage, bool) {
	if msgID == "" {
		return appapi.ChatMessage{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if m.ID == msgID {
			return m, true
		}
	}
	return appapi.ChatMessage{}, false
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
