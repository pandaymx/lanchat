package core

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/transfer"
)

const (
	maxHistoryMessages      = 500
	historyCompactThreshold = 2000
	historyFileName         = "lanchat-history.jsonl"
)

// historyStore 维护内存环形缓冲与可选的本地 JSONL 聊天历史文件（零 CGO，M23 支持 AES-GCM 静态加密落盘，v1.19 支持自动 Compaction、游标分页与媒体 CAS 落盘）。
type historyStore struct {
	mu        sync.Mutex
	dir       string
	atRestKey string
	msgs      []appapi.ChatMessage
	rawCipher map[string]string
	diskLines int
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
	// 放宽单行缓冲上限至 2 MiB，兼容含内联缩略图或语音的历史行。
	sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
	rawLines := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		rawLines++
		rawBytes, openErr := transfer.OpenHistoryLine(h.atRestKey, line)
		if openErr != nil {
			continue
		}
		var m appapi.ChatMessage
		if err := json.Unmarshal(rawBytes, &m); err == nil && m.ID != "" {
			if isSystemDiagnosticMessage(m) {
				continue
			}
			m.Text = sanitizeQuoteAndReactions(m.Text)
			populateStructuredFields(&m)
			h.externalizeMediaCASLocked(&m)
			if idx, ok := idxByID[m.ID]; ok {
				loaded[idx] = m
			} else if !existing[m.ID] {
				idxByID[m.ID] = len(loaded)
				loaded = append(loaded, m)
			}
		}
	}
	_ = f.Close()
	combined := append(loaded, h.msgs...)
	if len(combined) > maxHistoryMessages {
		combined = combined[len(combined)-maxHistoryMessages:]
	}
	h.msgs = combined
	h.diskLines = rawLines
	// 启动时若历史文件超过 Compaction 阈值或存在大量冗余变更行，自动重写合并。
	if rawLines >= historyCompactThreshold || (rawLines > len(h.msgs)+32 && len(h.msgs) > 0) {
		h.rewriteAllLocked()
	}
}

func (h *historyStore) append(m appapi.ChatMessage) bool {
	if isSystemDiagnosticMessage(m) {
		return false
	}
	m.Text = sanitizeQuoteAndReactions(m.Text)
	if m.TS == 0 {
		m.TS = time.Now().UnixMilli()
	}
	populateStructuredFields(&m)
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, existing := range h.msgs {
		if m.ID != "" && existing.ID == m.ID {
			return false
		}
	}
	h.externalizeMediaCASLocked(&m)
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
	if raw, err := json.Marshal(m); err == nil {
		line := string(raw)
		if h.atRestKey != "" {
			if sealed, sErr := transfer.SealHistoryLine(h.atRestKey, raw); sErr == nil {
				line = sealed
			}
		}
		_, _ = f.WriteString(line + "\n")
		h.diskLines++
	}
	_ = f.Close()
	if h.diskLines >= historyCompactThreshold {
		h.rewriteAllLocked()
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

// purgeByID 从内存与本地加密历史文件中彻底物理抹除指定消息（M30 阅后即焚自毁）。
func (h *historyStore) purgeByID(msgID string) bool {
	if msgID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found := false
	filtered := h.msgs[:0]
	for _, m := range h.msgs {
		if m.ID == msgID {
			found = true
			continue
		}
		filtered = append(filtered, m)
	}
	if !found {
		return false
	}
	h.msgs = append([]appapi.ChatMessage(nil), filtered...)
	delete(h.rawCipher, msgID)
	h.rewriteAllLocked()
	return true
}

func (h *historyStore) rewriteAllLocked() {
	if h.dir == "" {
		return
	}
	histPath := filepath.Join(h.dir, historyFileName)
	tmpPath := histPath + ".rewrite.tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	written := 0
	for _, m := range h.msgs {
		if raw, err := json.Marshal(m); err == nil {
			line := string(raw)
			if h.atRestKey != "" {
				if sealed, sErr := transfer.SealHistoryLine(h.atRestKey, raw); sErr == nil {
					line = sealed
				}
			}
			_, _ = f.WriteString(line + "\n")
			written++
		}
	}
	_ = f.Close()
	_ = os.Rename(tmpPath, histPath)
	h.diskLines = written
}

// compact 手动或自动触发 JSONL 历史文件 Compaction，返回合并消除的冗余行数（阶段三 v1.19）。
func (h *historyStore) compact() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.diskLines
	h.rewriteAllLocked()
	if before > h.diskLines {
		return before - h.diskLines
	}
	return 0
}

// page 按会话（convID 为空表示全部会话）与游标时间戳 beforeTS（0 表示从最新开始）分页返回历史消息（阶段三 v1.19）。
func (h *historyStore) page(convID string, beforeTS int64, limit int) []appapi.ChatMessage {
	if limit <= 0 {
		limit = 50
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	var matched []appapi.ChatMessage
	for _, m := range h.msgs {
		if convID != "" && m.Group != convID && m.From != convID && m.To != convID {
			continue
		}
		if beforeTS > 0 && m.TS >= beforeTS {
			continue
		}
		matched = append(matched, m)
	}
	if len(matched) > limit {
		matched = matched[len(matched)-limit:]
	}
	out := make([]appapi.ChatMessage, len(matched))
	copy(out, matched)
	return out
}

// externalizeMediaCASLocked 将内联 Base64 图片或语音载荷落盘至本地 CAS 目录（<dir>/cas/<sha256>.<ext>），填充 MediaMime 与 MediaPath（阶段三 v1.19）。
func (h *historyStore) externalizeMediaCASLocked(m *appapi.ChatMessage) {
	if m == nil || m.Revoked || h.dir == "" || m.MediaPath != "" {
		return
	}
	raw := m.Text
	var mimeType, b64Data string
	lower := strings.ToLower(raw)
	if idx := strings.Index(lower, "data:image/"); idx >= 0 {
		sub := raw[idx:]
		if semi := strings.Index(sub, ";base64,"); semi > len("data:") {
			mimeType = sub[len("data:"):semi]
			b64Data = strings.TrimSpace(sub[semi+len(";base64,"):])
			if sp := strings.IndexAny(b64Data, " \t\r\n"); sp > 0 {
				b64Data = b64Data[:sp]
			}
		}
	} else if idx := strings.Index(lower, "data:audio/"); idx >= 0 {
		sub := raw[idx:]
		if semi := strings.Index(sub, ";base64,"); semi > len("data:") {
			mimeType = sub[len("data:"):semi]
			b64Data = strings.TrimSpace(sub[semi+len(";base64,"):])
			if sp := strings.IndexAny(b64Data, " \t\r\n"); sp > 0 {
				b64Data = b64Data[:sp]
			}
		}
	}
	if mimeType == "" || b64Data == "" {
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil || len(decoded) == 0 {
		return
	}
	sum := sha256.Sum256(decoded)
	hashHex := hex.EncodeToString(sum[:])
	ext := ".bin"
	switch strings.ToLower(mimeType) {
	case "image/png":
		ext = ".png"
	case "image/jpeg", "image/jpg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "audio/mp4", "audio/aac", "audio/m4a":
		ext = ".m4a"
	case "audio/wav", "audio/x-wav":
		ext = ".wav"
	case "audio/ogg", "audio/opus":
		ext = ".ogg"
	}
	casDir := filepath.Join(h.dir, "cas")
	if err := os.MkdirAll(casDir, 0o755); err != nil {
		return
	}
	casPath := filepath.Join(casDir, hashHex+ext)
	if _, statErr := os.Stat(casPath); os.IsNotExist(statErr) {
		_ = os.WriteFile(casPath, decoded, 0o600)
	}
	m.MediaMime = mimeType
	m.MediaPath = casPath
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

// reactBy 为指定消息添加表情回应并持久化（M20 / 契约 v1.1 结构化 Reactions）。
func (h *historyStore) reactBy(msgID, actorID, emoji string) (appapi.ChatMessage, bool) {
	emoji = strings.TrimSpace(emoji)
	if msgID == "" || emoji == "" {
		return appapi.ChatMessage{}, false
	}
	if actorID == "" {
		actorID = "peer"
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
		if h.msgs[i].Reactions == nil {
			h.msgs[i].Reactions = make(map[string][]string)
		}
		users := h.msgs[i].Reactions[emoji]
		exists := false
		for _, u := range users {
			if u == actorID {
				exists = true
				break
			}
		}
		if !exists {
			h.msgs[i].Reactions[emoji] = append(users, actorID)
		}
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// lastMessageID 返回指定会话（单播 to 或群组 group）中最近一条未撤回消息的 ID。
func (h *historyStore) lastMessageID(to, group string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.msgs) - 1; i >= 0; i-- {
		m := h.msgs[i]
		if m.Revoked || m.ID == "" {
			continue
		}
		if group != "" {
			if m.Group == group {
				return m.ID
			}
			continue
		}
		if m.Group == "" && (m.From == to || m.To == to) {
			return m.ID
		}
	}
	return ""
}

func isReactionEmojiToken(token string) bool {
	if token == "" || len(token) > 24 {
		return false
	}
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			(r >= 0x4E00 && r <= 0x9FFF) || r == ':' || r == '"' || r == ' ' {
			return false
		}
	}
	return true
}

func splitReactionBadges(text string) (body, suffix string) {
	body = text
	for {
		idx := strings.LastIndex(body, " [")
		if idx < 0 || !strings.HasSuffix(body, "]") {
			break
		}
		token := body[idx+2 : len(body)-1]
		if !isReactionEmojiToken(token) {
			break
		}
		suffix = body[idx:] + suffix
		body = body[:idx]
	}
	return body, suffix
}

func isSystemDiagnosticText(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "[安全码指纹]") ||
		strings.HasPrefix(t, "🛡️ [安全码指纹") ||
		strings.HasPrefix(t, "📊 [诊断状态]") ||
		strings.HasPrefix(t, "🚀 [版本更新]") ||
		strings.HasPrefix(t, "⚡ [带宽限速")
}

func isSystemDiagnosticMessage(m appapi.ChatMessage) bool {
	for _, p := range []string{"fp-", "status-", "update-", "search-", "e2ee-", "bwlimit-"} {
		if strings.HasPrefix(m.ID, p) {
			return true
		}
	}
	return isSystemDiagnosticText(m.Text)
}

// sanitizeQuoteAndReactions 清理引用前缀 `「...」\n` 中遗留的 `[👍]` 表情回应后缀与系统诊断伪消息引用。
func sanitizeQuoteAndReactions(text string) string {
	body, suffix := splitReactionBadges(text)
	if strings.HasPrefix(body, "「") {
		if idx := strings.Index(body, "」\n"); idx > len("「") {
			quoted := body[len("「"):idx]
			rest := body[idx+len("」\n"):]
			cleanQuoted, _ := splitReactionBadges(quoted)
			if isSystemDiagnosticText(cleanQuoted) {
				body = rest
			} else {
				body = "「" + cleanQuoted + "」\n" + rest
			}
		}
	}
	return body + suffix
}

// edit 原地修改指定消息文本（校验发送者，保留置顶前缀与尾部表情回应，M20）。
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
		if strings.HasPrefix(h.msgs[i].Text, "📌 [置顶] ") || h.msgs[i].Pinned {
			prefix = "📌 [置顶] "
			h.msgs[i].Pinned = true
		}
		_, reactSuffix := splitReactionBadges(h.msgs[i].Text)
		h.msgs[i].Text = prefix + newText + " (已编辑)" + reactSuffix
		h.msgs[i].EditedAt = time.Now().UnixMilli()
		populateStructuredFields(&h.msgs[i])
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// pin 将指定消息标记为置顶公告并持久化（M20 / 契约 v1.1 Pinned）。
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
		h.msgs[i].Pinned = true
		updated := h.msgs[i]
		h.persistLocked(updated)
		return updated, true
	}
	return appapi.ChatMessage{}, false
}

// populateStructuredFields 从消息正文中提取结构化字段（ReplyQuote / Reactions / Pinned / MediaMime），保持向后兼容。
func populateStructuredFields(m *appapi.ChatMessage) {
	if m == nil || m.Revoked {
		return
	}
	body, suffix := splitReactionBadges(m.Text)
	if strings.HasPrefix(body, "📌 [置顶] ") {
		m.Pinned = true
		body = strings.TrimPrefix(body, "📌 [置顶] ")
	}
	if strings.HasPrefix(body, "「") {
		if idx := strings.Index(body, "」\n"); idx > len("「") {
			if m.ReplyQuote == "" {
				m.ReplyQuote = body[len("「"):idx]
			}
			body = body[idx+len("」\n"):]
		}
	}
	if suffix != "" {
		if m.Reactions == nil {
			m.Reactions = make(map[string][]string)
		}
		for _, part := range strings.Split(strings.TrimSpace(suffix), " ") {
			if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") && len(part) > 2 {
				emoji := part[1 : len(part)-1]
				if isReactionEmojiToken(emoji) && len(m.Reactions[emoji]) == 0 {
					actor := m.From
					if actor == "" {
						actor = "peer"
					}
					m.Reactions[emoji] = []string{actor}
				}
			}
		}
	}
	trimmedBody := strings.TrimSpace(body)
	if m.MediaMime == "" {
		if strings.HasPrefix(strings.ToLower(trimmedBody), "data:image/") {
			if semi := strings.IndexByte(trimmedBody, ';'); semi > len("data:") {
				m.MediaMime = trimmedBody[len("data:"):semi]
			}
		} else if audioIdx := strings.Index(strings.ToLower(trimmedBody), "data:audio/"); audioIdx >= 0 {
			sub := trimmedBody[audioIdx:]
			if semi := strings.IndexByte(sub, ';'); semi > len("data:") {
				m.MediaMime = sub[len("data:"):semi]
			}
		}
	}
}

// search 在本地历史中按多维过滤条件（from:<peer>、type:image|voice|code|pin、since:<ts>）及关键字（大小写不敏感）与可选会话 ID 检索未撤回消息（v3.4.0）。
func (h *historyStore) search(query, convID string) []appapi.ChatMessage {
	rawTokens := strings.Fields(strings.TrimSpace(query))
	if len(rawTokens) == 0 {
		return nil
	}
	var (
		fromFilter string
		typeFilter string
		sinceTS    int64
		keywords   []string
	)
	for _, tk := range rawTokens {
		lower := strings.ToLower(tk)
		switch {
		case strings.HasPrefix(lower, "from:"):
			fromFilter = strings.TrimPrefix(lower, "from:")
		case strings.HasPrefix(lower, "type:"):
			typeFilter = strings.TrimPrefix(lower, "type:")
		case strings.HasPrefix(lower, "since:"):
			if v, err := strconv.ParseInt(strings.TrimPrefix(lower, "since:"), 10, 64); err == nil && v > 0 {
				sinceTS = v
			}
		default:
			keywords = append(keywords, lower)
		}
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
		if fromFilter != "" && !strings.Contains(strings.ToLower(m.From), fromFilter) {
			continue
		}
		if sinceTS > 0 && m.TS < sinceTS {
			continue
		}
		lowerText := strings.ToLower(m.Text)
		lowerMime := strings.ToLower(m.MediaMime)
		if typeFilter != "" {
			switch typeFilter {
			case "image", "img", "photo":
				if !strings.HasPrefix(lowerMime, "image/") && !strings.Contains(lowerText, "data:image/") {
					continue
				}
			case "voice", "audio":
				if !strings.HasPrefix(lowerMime, "audio/") && !strings.Contains(lowerText, "data:audio/") && !strings.Contains(m.Text, "[语音") {
					continue
				}
			case "code":
				if !strings.Contains(m.Text, "```") {
					continue
				}
			case "pin", "pinned":
				if !m.Pinned && !strings.HasPrefix(m.Text, "📌 [置顶]") {
					continue
				}
			}
		}
		matchedAll := true
		for _, kw := range keywords {
			if !strings.Contains(lowerText, kw) && !strings.Contains(strings.ToLower(m.ReplyQuote), kw) {
				matchedAll = false
				break
			}
		}
		if matchedAll {
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

// exportSince 导出 TS >= sinceTs 的本地历史消息切片（用于 v3.0.0 多端漫游增量同步）。
func (h *historyStore) exportSince(sinceTs int64) []appapi.ChatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []appapi.ChatMessage
	for _, m := range h.msgs {
		if m.TS >= sinceTs {
			out = append(out, m)
		}
	}
	return out
}

// mergeRemoteMessages 幂等合并来自个人其他设备的历史漫游消息（按 MsgID 去重或更新状态，返回新增/更新条数，v3.0.0）。
func (h *historyStore) mergeRemoteMessages(incoming []appapi.ChatMessage) int {
	if len(incoming) == 0 {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	idxByID := make(map[string]int, len(h.msgs))
	for i, m := range h.msgs {
		if m.ID != "" {
			idxByID[m.ID] = i
		}
	}
	changed := 0
	for _, rm := range incoming {
		if rm.ID == "" {
			continue
		}
		populateStructuredFields(&rm)
		if idx, exists := idxByID[rm.ID]; exists {
			cur := h.msgs[idx]
			if rm.Revoked && !cur.Revoked {
				h.msgs[idx].Revoked = true
				h.msgs[idx].Text = "[消息已撤回]"
				h.persistLocked(h.msgs[idx])
				changed++
			} else if rm.EditedAt > cur.EditedAt || (rm.Pinned && !cur.Pinned) {
				h.msgs[idx] = rm
				h.persistLocked(h.msgs[idx])
				changed++
			}
			continue
		}
		h.msgs = append(h.msgs, rm)
		idxByID[rm.ID] = len(h.msgs) - 1
		h.persistLocked(rm)
		changed++
	}
	if len(h.msgs) > maxHistoryMessages {
		h.msgs = append([]appapi.ChatMessage(nil), h.msgs[len(h.msgs)-maxHistoryMessages:]...)
	}
	return changed
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
