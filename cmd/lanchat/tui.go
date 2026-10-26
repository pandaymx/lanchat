package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/core"
)

// ANSI 终端控制序列与配色常量。
const (
	ansiClearScreen = "\x1b[2J\x1b[H"
	ansiAltEnter    = "\x1b[?1049h\x1b[?25l"
	ansiAltExit     = "\x1b[?25h\x1b[?1049l"
	ansiReset       = "\x1b[0m"
	ansiBold        = "\x1b[1m"
	ansiDim         = "\x1b[2m"
	ansiCyan        = "\x1b[36m"
	ansiGreen       = "\x1b[32m"
	ansiYellow      = "\x1b[33m"
	ansiRed         = "\x1b[31m"
	ansiMagenta     = "\x1b[35m"
	ansiBgBlue      = "\x1b[44;37;1m"
	ansiBgSelect    = "\x1b[48;5;237m\x1b[36;1m"
)

// tuiConvItem 表示左侧会话栏中的一个会话项（全体广播、频道或 1:1 在线成员）。
type tuiConvItem struct {
	ID       string
	Title    string
	Subtitle string
	IsGroup  bool
}

// tuiReactionBadge 表示聚合后的消息下方表情回应计数。
type tuiReactionBadge struct {
	Emoji string
	Count int
}

// tuiParsedMessage 表示拆分引用块、正文与下方反应徽章后的消息结构。
type tuiParsedMessage struct {
	QuoteText string
	BodyText  string
	Reactions []tuiReactionBadge
}

var knownTUIReactionEmojis = map[string]bool{
	"👍": true, "❤️": true, "❤": true, "😂": true,
	"🎉": true, "🔥": true, "👏": true, "😮": true, "😢": true,
}

// parseTUIMessageParts 拆分消息中的「引用内容」、正文与末尾的 [👍] 等表情反应，并过滤安全码等诊断文本。
func parseTUIMessageParts(raw string) tuiParsedMessage {
	remain := strings.TrimSpace(raw)
	var rawTokens []string

	for {
		trimmed := strings.TrimRight(remain, " \t")
		openIdx := strings.LastIndex(trimmed, "[")
		if openIdx < 0 || !strings.HasSuffix(trimmed, "]") {
			break
		}
		token := strings.TrimSpace(trimmed[openIdx+1 : len(trimmed)-1])
		if !knownTUIReactionEmojis[token] {
			break
		}
		if token == "❤" {
			token = "❤️"
		}
		rawTokens = append(rawTokens, token)
		remain = strings.TrimRight(trimmed[:openIdx], " \t")
	}

	var counts []tuiReactionBadge
	indexByEmoji := map[string]int{}
	for i := len(rawTokens) - 1; i >= 0; i-- {
		token := rawTokens[i]
		if idx, ok := indexByEmoji[token]; ok {
			counts[idx].Count++
		} else {
			indexByEmoji[token] = len(counts)
			counts = append(counts, tuiReactionBadge{Emoji: token, Count: 1})
		}
	}

	var quoteText string
	bodyText := remain
	if strings.HasPrefix(remain, "「") {
		if closeIdx := strings.Index(remain, "」\n"); closeIdx > len("「") {
			q := strings.TrimSpace(remain[len("「"):closeIdx])
			rest := strings.TrimSpace(remain[closeIdx+len("」\n"):])
			if !isTUIDiagnosticText(q) {
				quoteText = q
			}
			bodyText = rest
		}
	}

	return tuiParsedMessage{
		QuoteText: quoteText,
		BodyText:  bodyText,
		Reactions: counts,
	}
}

func isTUIDiagnosticText(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "[安全码指纹]") ||
		strings.HasPrefix(t, "status:conn=") ||
		strings.HasPrefix(t, "fp:") ||
		strings.HasPrefix(t, "sys:")
}

// runeDisplayWidth 计算终端中单个 Unicode 字符的显示列宽（CJK 宽字符与 Emoji 占 2 列）。
func runeDisplayWidth(r rune) int {
	if r == 0 || r == 0xFE0F || r == 0x200D || r < 0x20 {
		return 0
	}
	if (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF) ||
		(r >= 0x2600 && r <= 0x27BF) {
		return 2
	}
	return 1
}

func stringDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

// padOrTruncateDisplay 将纯文本按终端显示列宽截断或右侧补空格至 targetWidth 列。
func padOrTruncateDisplay(s string, targetWidth int) string {
	if targetWidth <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if cur+rw > targetWidth {
			if cur < targetWidth {
				b.WriteByte(' ')
				cur++
			}
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	if cur < targetWidth {
		b.WriteString(strings.Repeat(" ", targetWidth-cur))
	}
	return b.String()
}

// wrapDisplayLines 将多行文本按给定的最大终端列宽自动折行。
func wrapDisplayLines(s string, maxWidth int) []string {
	if maxWidth <= 2 {
		return []string{s}
	}
	rawLines := strings.Split(s, "\n")
	var out []string
	for _, line := range rawLines {
		if line == "" {
			out = append(out, "")
			continue
		}
		var b strings.Builder
		cur := 0
		for _, r := range line {
			rw := runeDisplayWidth(r)
			if cur+rw > maxWidth && cur > 0 {
				out = append(out, b.String())
				b.Reset()
				cur = 0
			}
			b.WriteRune(r)
			cur += rw
		}
		if b.Len() > 0 {
			out = append(out, b.String())
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// tuiModel 维护全屏 TUI 的状态、会话切换、消息滚动与撤回重编辑缓冲。
type tuiModel struct {
	mu               sync.Mutex
	client           *core.Client
	downloadDir      string
	activeTarget     string
	scrollOffset     int
	inputBuf         []rune
	historyCmds      []string
	historyIdx       int
	lastRecalledText string
	statusBanner     string
	notifyCh         chan struct{}
}

func newTUIModel(client *core.Client, downloadDir string) *tuiModel {
	return &tuiModel{
		client:       client,
		downloadDir:  downloadDir,
		activeTarget: "*",
		historyIdx:   -1,
		statusBanner: "就绪 · Tab/↑↓ 切换会话 · /help 查看指令 · /recall 撤回 · /reedit 重新编辑 · /update 升级",
		notifyCh:     make(chan struct{}, 1),
	}
}

func (m *tuiModel) triggerRedraw() {
	if m == nil {
		return
	}
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
}

func (m *tuiModel) setStatus(msg string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.statusBanner = msg
	m.mu.Unlock()
	m.triggerRedraw()
}

// conversations 根据当前核心状态组装左侧会话列表（全体广播 + 频道 + 在线成员）。
func (m *tuiModel) conversations(st appapi.State) []tuiConvItem {
	items := []tuiConvItem{
		{
			ID:       "*",
			Title:    "全体广播",
			Subtitle: fmt.Sprintf("%d 人在线", len(st.Peers)+1),
			IsGroup:  true,
		},
	}
	for _, ch := range st.Channels {
		name := ch.Name
		if name == "" {
			name = ch.ID
		}
		items = append(items, tuiConvItem{
			ID:       ch.ID,
			Title:    "# " + name,
			Subtitle: fmt.Sprintf("%d 成员", len(ch.Members)),
			IsGroup:  true,
		})
	}
	for _, p := range st.Peers {
		nick := p.Nickname
		if nick == "" {
			nick = p.ID
		}
		items = append(items, tuiConvItem{
			ID:       p.ID,
			Title:    "• " + nick,
			Subtitle: p.OS,
			IsGroup:  false,
		})
	}
	return items
}

func (m *tuiModel) cycleConversation(delta int) {
	st := m.client.GetState()
	items := m.conversations(st)
	if len(items) == 0 {
		return
	}
	m.mu.Lock()
	curIdx := 0
	for i, it := range items {
		if it.ID == m.activeTarget {
			curIdx = i
			break
		}
	}
	nextIdx := (curIdx + delta + len(items)) % len(items)
	m.activeTarget = items[nextIdx].ID
	m.scrollOffset = 0
	m.statusBanner = fmt.Sprintf("已切换至会话: %s (%s)", items[nextIdx].Title, items[nextIdx].ID)
	m.mu.Unlock()
	m.triggerRedraw()
}

// filterMessagesForTarget 过滤当前选中会话的消息列表，剔除安全码指纹等诊断输出。
func filterMessagesForTarget(st appapi.State, target string) []appapi.ChatMessage {
	var out []appapi.ChatMessage
	for _, msg := range st.Messages {
		if !msg.Revoked && isTUIDiagnosticText(msg.Text) {
			continue
		}
		if target == "*" {
			if msg.Group == "*" {
				out = append(out, msg)
			}
			continue
		}
		isChannel := false
		for _, ch := range st.Channels {
			if ch.ID == target {
				isChannel = true
				break
			}
		}
		if isChannel {
			if msg.Group == target {
				out = append(out, msg)
			}
			continue
		}
		if msg.Group == "" && (msg.From == target || msg.To == target) {
			out = append(out, msg)
		}
	}
	return out
}

func resolveSenderLabel(st appapi.State, peerID string) string {
	if peerID == st.SelfID || peerID == "me" {
		if st.Nickname != "" {
			return st.Nickname + " (我)"
		}
		return "我"
	}
	for _, p := range st.Peers {
		if p.ID == peerID && p.Nickname != "" {
			return p.Nickname
		}
	}
	if len(peerID) > 8 {
		return peerID[:8]
	}
	return peerID
}

// renderFrame 渲染一帧完整的全屏 TUI 字符串（纯函数式给定宽高，便于单元测试与终端输出）。
func (m *tuiModel) renderFrame(width, height int) string {
	if width < 48 {
		width = 48
	}
	if height < 14 {
		height = 14
	}
	st := m.client.GetState()
	convs := m.conversations(st)

	m.mu.Lock()
	activeTarget := m.activeTarget
	scrollOffset := m.scrollOffset
	inputStr := string(m.inputBuf)
	statusBanner := m.statusBanner
	m.mu.Unlock()

	activeTitle := "全体广播 (*)"
	for _, it := range convs {
		if it.ID == activeTarget {
			activeTitle = it.Title
			break
		}
	}

	var buf bytes.Buffer
	buf.WriteString("\x1b[H")

	// 1. 顶部状态栏（1 行）
	connBadge := "● 已连接"
	switch st.Conn {
	case appapi.ConnConnecting:
		connBadge = "◌ 连接中"
	case appapi.ConnDisconnected, appapi.ConnAuthFailed:
		connBadge = "× 未连接"
	}
	fp := m.client.Fingerprint()
	headerPlain := fmt.Sprintf(" LANChat TUI %s │ %s │ 当前会话: %s │ 本机: %s │ 安全码: %s",
		version, connBadge, activeTitle, orDefault(st.Nickname, st.SelfID), fp)
	buf.WriteString(ansiBgBlue + padOrTruncateDisplay(headerPlain, width) + ansiReset + "\r\n")

	// 计算活跃传输条占用的底部行数（最多展示 2 条传输）
	var activeTransfers []appapi.Transfer
	for _, tr := range st.Transfers {
		if tr.State == appapi.TransferPending || tr.State == appapi.TransferActive || tr.State == appapi.TransferPaused {
			activeTransfers = append(activeTransfers, tr)
			if len(activeTransfers) >= 2 {
				break
			}
		}
	}

	// 底部固定占 3 行（分隔状态栏 + 快捷键提示栏 + 输入框）+ 活跃传输行数
	bottomRows := 3 + len(activeTransfers)
	bodyHeight := height - 1 - bottomRows
	if bodyHeight < 6 {
		bodyHeight = 6
	}

	sidebarWidth := 24
	if width < 70 {
		sidebarWidth = 18
	}
	chatWidth := width - sidebarWidth - 1

	// 2. 组装左侧会话栏行（长度固定为 bodyHeight）
	leftLines := make([]string, bodyHeight)
	leftLines[0] = ansiBold + padOrTruncateDisplay(" 会话列表 (Tab切换)", sidebarWidth) + ansiReset
	leftLines[1] = ansiDim + strings.Repeat("─", sidebarWidth) + ansiReset
	row := 2
	for _, it := range convs {
		if row >= bodyHeight {
			break
		}
		prefix := "  "
		if it.ID == activeTarget {
			prefix = "▸ "
		}
		lineText := padOrTruncateDisplay(prefix+it.Title, sidebarWidth)
		if it.ID == activeTarget {
			leftLines[row] = ansiBgSelect + lineText + ansiReset
		} else {
			leftLines[row] = lineText
		}
		row++
	}
	for ; row < bodyHeight; row++ {
		leftLines[row] = strings.Repeat(" ", sidebarWidth)
	}

	// 3. 组装右侧消息流（QQ 气泡风：撤回居中灰条 + 引用框 + 气泡正文 + 气泡正下方表情回应胶囊）
	msgs := filterMessagesForTarget(st, activeTarget)
	var chatRendered []string
	if len(msgs) == 0 {
		chatRendered = append(chatRendered,
			"",
			ansiDim+padOrTruncateDisplay("  暂无聊天记录，直接输入文字回车发送，或输入 /help 查看命令。", chatWidth)+ansiReset,
		)
	} else {
		for _, msg := range msgs {
			isSelf := msg.From == st.SelfID || msg.From == "me"
			if msg.Revoked {
				var pill string
				if isSelf {
					pill = "── 你撤回了一条消息 (输入 /reedit 重新编辑) ──"
				} else {
					pill = fmt.Sprintf("── %s 撤回了一条消息 ──", resolveSenderLabel(st, msg.From))
				}
				padLeft := (chatWidth - stringDisplayWidth(pill)) / 2
				if padLeft < 1 {
					padLeft = 1
				}
				centered := strings.Repeat(" ", padLeft) + pill
				chatRendered = append(chatRendered, ansiDim+padOrTruncateDisplay(centered, chatWidth)+ansiReset)
				chatRendered = append(chatRendered, strings.Repeat(" ", chatWidth))
				continue
			}

			tsStr := time.UnixMilli(msg.TS).Format("15:04:05")
			if msg.TS <= 0 {
				tsStr = "--:--:--"
			}
			shortID := msg.ID
			if len(shortID) > 6 {
				shortID = shortID[:6]
			}
			sender := resolveSenderLabel(st, msg.From)
			metaPlain := fmt.Sprintf(" %s [%s] #%s", sender, tsStr, shortID)
			if isSelf {
				chatRendered = append(chatRendered, ansiGreen+ansiBold+padOrTruncateDisplay(metaPlain, chatWidth)+ansiReset)
			} else {
				chatRendered = append(chatRendered, ansiCyan+ansiBold+padOrTruncateDisplay(metaPlain, chatWidth)+ansiReset)
			}

			parsed := parseTUIMessageParts(msg.Text)
			if parsed.QuoteText != "" {
				for _, ql := range wrapDisplayLines(parsed.QuoteText, chatWidth-8) {
					qRow := "   ╭ 引用: " + ql
					chatRendered = append(chatRendered, ansiDim+padOrTruncateDisplay(qRow, chatWidth)+ansiReset)
				}
			}

			bodyLines := wrapDisplayLines(parsed.BodyText, chatWidth-6)
			for _, bl := range bodyLines {
				bRow := "   │ " + bl
				chatRendered = append(chatRendered, padOrTruncateDisplay(bRow, chatWidth))
			}

			// 表情回应胶囊独立渲染在气泡下方
			if len(parsed.Reactions) > 0 {
				var chips []string
				for _, rc := range parsed.Reactions {
					chips = append(chips, fmt.Sprintf("[ %s %d ]", rc.Emoji, rc.Count))
				}
				reactLine := "   ╰─ " + strings.Join(chips, " ")
				chatRendered = append(chatRendered, ansiYellow+padOrTruncateDisplay(reactLine, chatWidth)+ansiReset)
			}
			chatRendered = append(chatRendered, strings.Repeat(" ", chatWidth))
		}
	}

	// 计算滚动窗口
	totalChatLines := len(chatRendered)
	endIdx := totalChatLines - scrollOffset
	if endIdx > totalChatLines {
		endIdx = totalChatLines
	}
	if endIdx < bodyHeight {
		endIdx = bodyHeight
		if endIdx > totalChatLines {
			endIdx = totalChatLines
		}
	}
	startIdx := endIdx - bodyHeight
	if startIdx < 0 {
		startIdx = 0
	}

	rightLines := make([]string, bodyHeight)
	idx := 0
	for i := startIdx; i < endIdx && idx < bodyHeight; i++ {
		rightLines[idx] = chatRendered[i]
		idx++
	}
	for ; idx < bodyHeight; idx++ {
		rightLines[idx] = strings.Repeat(" ", chatWidth)
	}

	// 4. 拼接左右分栏主体
	for i := 0; i < bodyHeight; i++ {
		buf.WriteString(leftLines[i])
		buf.WriteString(ansiDim + "│" + ansiReset)
		buf.WriteString(rightLines[i])
		buf.WriteString("\r\n")
	}

	// 5. 渲染活跃文件传输进度条（若有）
	for _, tr := range activeTransfers {
		var pct int64
		if tr.Size > 0 {
			pct = (tr.BytesDone * 100) / tr.Size
		}
		barWidth := 12
		filled := int(pct) * barWidth / 100
		if filled > barWidth {
			filled = barWidth
		}
		bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + "]"
		modeTag := "直连"
		if tr.ViaRelay {
			modeTag = "⚠中继"
		}
		shortTID := tr.ID
		if len(shortTID) > 6 {
			shortTID = shortTID[:6]
		}
		var trLine string
		if tr.Direction == appapi.TransferInbound && tr.State == appapi.TransferPending {
			trLine = fmt.Sprintf(" 📎 [待接收] %s (%d B) 来自 %s -> 输入 /accept %s 接收",
				tr.Name, tr.Size, tr.PeerID, tr.ID)
		} else {
			speedMB := float64(tr.SpeedBps) / (1024 * 1024)
			trLine = fmt.Sprintf(" 📎 %s %3d%% (%.1f MB/s, %s) %s [id=%s]",
				bar, pct, speedMB, modeTag, tr.Name, shortTID)
		}
		buf.WriteString(ansiMagenta + padOrTruncateDisplay(trLine, width) + ansiReset + "\r\n")
	}

	// 6. 状态通知栏 + 快捷键提示栏 + 底部输入框
	statusRow := " ℹ " + statusBanner
	buf.WriteString(ansiYellow + padOrTruncateDisplay(statusRow, width) + ansiReset + "\r\n")

	hintRow := " 快捷键: Tab:切会话 │ PgUp/PgDn:翻页 │ /recall:撤回 │ /reedit:重编辑 │ /react 👍:点赞 │ /send:发文件 │ /update:升级 │ /quit:退出"
	buf.WriteString(ansiDim + padOrTruncateDisplay(hintRow, width) + ansiReset + "\r\n")

	promptPrefix := fmt.Sprintf(" [%s] > ", activeTitle)
	availInputWidth := width - stringDisplayWidth(promptPrefix) - 2
	if availInputWidth < 10 {
		availInputWidth = 10
	}
	visibleInput := inputStr
	for stringDisplayWidth(visibleInput) > availInputWidth {
		_, sz := utf8.DecodeRuneInString(visibleInput)
		visibleInput = visibleInput[sz:]
	}
	inputRow := promptPrefix + visibleInput + "█"
	buf.WriteString(ansiBold + padOrTruncateDisplay(inputRow, width) + ansiReset)

	return buf.String()
}

// executeTUICommand 处理用户在 TUI 或增强 CLI 中输入的一行命令或聊天消息。
func (m *tuiModel) executeTUICommand(rawLine string) (shouldQuit bool) {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return false
	}

	m.mu.Lock()
	m.historyCmds = append(m.historyCmds, line)
	m.historyIdx = len(m.historyCmds)
	target := m.activeTarget
	m.mu.Unlock()

	switch {
	case line == "/quit" || line == "/exit":
		return true

	case line == "/help":
		m.setStatus("指令: /to <目标> | /recall [ID] | /reedit | /react [👍] | /quote <内容> | /send <文件> | /accept [ID] | /create <名> | /join <ID> | /update [self|check]")
		return false

	case line == "/reedit":
		m.mu.Lock()
		recalled := m.lastRecalledText
		if recalled != "" {
			m.inputBuf = []rune(recalled)
			m.statusBanner = "已将刚撤回的消息填入输入框，修改后按回车重新发送"
		} else {
			m.statusBanner = "暂无可重新编辑的撤回消息"
		}
		m.mu.Unlock()
		m.triggerRedraw()
		return false

	case line == "/peers":
		st := m.client.GetState()
		if len(st.Peers) == 0 {
			m.setStatus("当前暂无其他在线成员")
			return false
		}
		var names []string
		for _, p := range st.Peers {
			names = append(names, fmt.Sprintf("%s(%s)", orDefault(p.Nickname, p.ID), p.ID))
		}
		m.setStatus("在线成员: " + strings.Join(names, ", "))
		return false

	case line == "/channels":
		st := m.client.GetState()
		if len(st.Channels) == 0 {
			m.setStatus("当前暂无频道，可使用 /create <名称> 创建")
			return false
		}
		var chs []string
		for _, ch := range st.Channels {
			chs = append(chs, fmt.Sprintf("#%s(%s,%d人)", ch.Name, ch.ID, len(ch.Members)))
		}
		m.setStatus("频道列表: " + strings.Join(chs, ", "))
		return false

	case strings.HasPrefix(line, "/to "):
		arg := strings.TrimSpace(strings.TrimPrefix(line, "/to "))
		st := m.client.GetState()
		convs := m.conversations(st)
		matchedID := arg
		if idx, err := strconv.Atoi(arg); err == nil && idx >= 1 && idx <= len(convs) {
			matchedID = convs[idx-1].ID
		} else {
			for _, it := range convs {
				if strings.EqualFold(it.ID, arg) || strings.Contains(strings.ToLower(it.Title), strings.ToLower(arg)) {
					matchedID = it.ID
					break
				}
			}
		}
		m.mu.Lock()
		m.activeTarget = matchedID
		m.scrollOffset = 0
		m.mu.Unlock()
		m.setStatus("已切换当前会话目标 -> " + matchedID)
		return false

	case line == "/recall" || strings.HasPrefix(line, "/recall ") || strings.HasPrefix(line, "/revoke "):
		arg := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "/recall"), "/revoke"))
		st := m.client.GetState()
		msgs := filterMessagesForTarget(st, target)
		targetMsgID := arg
		var recalledBody string
		if targetMsgID == "" {
			for i := len(msgs) - 1; i >= 0; i-- {
				if (msgs[i].From == st.SelfID || msgs[i].From == "me") && !msgs[i].Revoked {
					targetMsgID = msgs[i].ID
					recalledBody = parseTUIMessageParts(msgs[i].Text).BodyText
					break
				}
			}
		} else {
			for i := len(msgs) - 1; i >= 0; i-- {
				if (msgs[i].ID == targetMsgID || strings.HasPrefix(msgs[i].ID, targetMsgID)) && !msgs[i].Revoked {
					targetMsgID = msgs[i].ID
					recalledBody = parseTUIMessageParts(msgs[i].Text).BodyText
					break
				}
			}
		}
		if targetMsgID == "" {
			m.setStatus("未找到可撤回的本机消息（仅支持撤回 2 分钟内发送的消息）")
			return false
		}
		to, group := resolveTargetToAndGroup(m.client, target)
		if _, err := m.client.SendText(to, "/revoke "+targetMsgID, group); err != nil {
			m.setStatus("撤回失败: " + err.Error())
			return false
		}
		m.mu.Lock()
		if recalledBody != "" {
			m.lastRecalledText = recalledBody
		}
		m.mu.Unlock()
		m.setStatus("已撤回消息，输入 /reedit 可重新编辑")
		return false

	case strings.HasPrefix(line, "/quote "):
		body := strings.TrimSpace(strings.TrimPrefix(line, "/quote "))
		st := m.client.GetState()
		msgs := filterMessagesForTarget(st, target)
		if len(msgs) == 0 || body == "" {
			m.setStatus("用法: /quote <回复内容>（自动引用当前会话最后一条消息）")
			return false
		}
		lastID := msgs[len(msgs)-1].ID
		to, group := resolveTargetToAndGroup(m.client, target)
		if _, err := m.client.SendText(to, fmt.Sprintf("/reply %s %s", lastID, body), group); err != nil {
			m.setStatus("引用回复失败: " + err.Error())
		} else {
			m.setStatus("已发送引用回复")
		}
		return false

	case strings.HasPrefix(line, "/nick "):
		newNick := strings.TrimSpace(strings.TrimPrefix(line, "/nick "))
		if err := m.client.SetNickname(newNick); err != nil {
			m.setStatus("修改昵称失败: " + err.Error())
		} else {
			m.setStatus("已修改本机昵称为: " + newNick)
		}
		return false

	case strings.HasPrefix(line, "/create "):
		name := strings.TrimSpace(strings.TrimPrefix(line, "/create "))
		chID, err := m.client.ChannelCreate(name, "", false)
		if err != nil {
			m.setStatus("创建频道失败: " + err.Error())
		} else {
			m.mu.Lock()
			m.activeTarget = chID
			m.mu.Unlock()
			m.setStatus(fmt.Sprintf("已创建并进入频道 #%s (%s)", name, chID))
		}
		return false

	case strings.HasPrefix(line, "/join "):
		chID := strings.TrimSpace(strings.TrimPrefix(line, "/join "))
		if err := m.client.ChannelJoin(chID); err != nil {
			m.setStatus("加入频道失败: " + err.Error())
		} else {
			m.mu.Lock()
			m.activeTarget = chID
			m.mu.Unlock()
			m.setStatus("已加入频道: " + chID)
		}
		return false

	case line == "/leave" || strings.HasPrefix(line, "/leave "):
		chID := strings.TrimSpace(strings.TrimPrefix(line, "/leave"))
		if chID == "" {
			chID = target
		}
		if err := m.client.ChannelLeave(chID); err != nil {
			m.setStatus("离开频道失败: " + err.Error())
		} else {
			m.mu.Lock()
			if m.activeTarget == chID {
				m.activeTarget = "*"
			}
			m.mu.Unlock()
			m.setStatus("已离开频道: " + chID)
		}
		return false

	case strings.HasPrefix(line, "/send ") || strings.HasPrefix(line, "/senddir ") || strings.HasPrefix(line, "/folder "):
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "/senddir"), "/folder"), "/send"))
		var err error
		if target == "*" || isChannelTarget(m.client, target) {
			_, err = m.client.OfferFileToGroup(target, path)
		} else {
			_, err = m.client.OfferFile(target, path)
		}
		if err != nil {
			m.setStatus("发送失败: " + err.Error())
		} else if st, statErr := os.Stat(path); statErr == nil && st.IsDir() {
			m.setStatus("已发起文件夹目录树流式传输: " + filepath.Base(path))
		} else {
			m.setStatus("已发起文件传输: " + filepath.Base(path))
		}
		return false

	case line == "/accept" || strings.HasPrefix(line, "/accept "):
		tid := strings.TrimSpace(strings.TrimPrefix(line, "/accept"))
		st := m.client.GetState()
		if tid == "" {
			for _, tr := range st.Transfers {
				if tr.Direction == appapi.TransferInbound && tr.State == appapi.TransferPending {
					tid = tr.ID
					break
				}
			}
		} else {
			for _, tr := range st.Transfers {
				if tr.ID == tid || strings.HasPrefix(tr.ID, tid) {
					tid = tr.ID
					break
				}
			}
		}
		if tid == "" {
			m.setStatus("当前没有待接收的文件传输")
			return false
		}
		dest := filepath.Join(m.downloadDir, "received-"+tid)
		for _, tr := range st.Transfers {
			if tr.ID == tid && tr.Name != "" {
				dest = filepath.Join(m.downloadDir, filepath.Base(tr.Name))
				break
			}
		}
		if err := m.client.RespondFile(tid, true, dest); err != nil {
			m.setStatus("接收文件失败: " + err.Error())
		} else {
			m.setStatus("开始接收文件 -> " + dest)
		}
		return false
	}

	to, group := resolveTargetToAndGroup(m.client, target)
	res, err := m.client.SendText(to, line, group)
	if err != nil {
		m.setStatus("操作失败: " + err.Error())
		return false
	}
	if strings.HasPrefix(res, "update:") || strings.HasPrefix(res, "status:") || strings.HasPrefix(res, "fp:") || strings.HasPrefix(res, "bwlimit:") || strings.HasPrefix(res, "e2ee:") || strings.HasPrefix(res, "clip:") {
		m.setStatus(res)
	} else {
		m.mu.Lock()
		m.scrollOffset = 0
		m.mu.Unlock()
		m.triggerRedraw()
	}
	return false
}

func resolveTargetToAndGroup(c *core.Client, target string) (to, group string) {
	if target == "*" || isChannelTarget(c, target) {
		return "", target
	}
	return target, ""
}

type tuiEventListener struct {
	model *tuiModel
}

func (l *tuiEventListener) OnConnChanged(state appapi.ConnState, reason string) {
	if reason != "" {
		l.model.setStatus(fmt.Sprintf("连接状态: %s (%s)", state, reason))
		return
	}
	l.model.setStatus(fmt.Sprintf("连接状态: %s", state))
}

func (l *tuiEventListener) OnPeerJoined(peer appapi.Peer) {
	l.model.setStatus(fmt.Sprintf("成员上线: %s (%s)", orDefault(peer.Nickname, peer.ID), peer.OS))
}

func (l *tuiEventListener) OnPeerLeft(peerID string) {
	l.model.setStatus(fmt.Sprintf("成员离线: %s", peerID))
}

func (l *tuiEventListener) OnMessageReceived(_, _, _, typ, text string) {
	if typ == "CLIP_SYNC" {
		l.model.setStatus(text)
		return
	}
	l.model.triggerRedraw()
}

func (l *tuiEventListener) OnTransferProgress(tr appapi.Transfer) {
	if tr.Direction == appapi.TransferInbound && tr.State == appapi.TransferPending {
		l.model.setStatus(fmt.Sprintf("收到文件邀请: %s (%d B) -> 输入 /accept 接收", tr.Name, tr.Size))
		return
	}
	l.model.triggerRedraw()
}

func (l *tuiEventListener) OnTransferDone(transferID string) {
	l.model.setStatus("文件传输完成: " + transferID)
}

func (l *tuiEventListener) OnTransferFailed(transferID, reason string) {
	l.model.setStatus(fmt.Sprintf("文件传输失败 (%s): %s", transferID, reason))
}

func (l *tuiEventListener) OnGroupMatrix(_, _ string, _ []byte) {
	l.model.triggerRedraw()
}

func (l *tuiEventListener) OnChannelUpdated(channels []appapi.Channel) {
	l.model.setStatus(fmt.Sprintf("频道列表已同步（共 %d 个频道）", len(channels)))
}

// runInteractiveTUI 启动终端全屏交互式 TUI 界面。
func runInteractiveTUI(ctx context.Context, opts cliOptions) error {
	fd := int(os.Stdin.Fd())
	outFd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) || !term.IsTerminal(outFd) {
		return runCLISession(ctx, os.Stdin, os.Stdout, opts)
	}

	listener := &tuiEventListener{}
	client := core.New(core.Options{
		Nickname:    opts.nick,
		OS:          runtime.GOOS,
		DownloadDir: opts.downloadDir,
		Listener:    listener,
	})
	defer client.Close()

	model := newTUIModel(client, opts.downloadDir)
	listener.model = model

	if err := client.Connect(opts.addr, opts.psk); err != nil {
		return err
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return runCLISession(ctx, os.Stdin, os.Stdout, opts)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		_, _ = io.WriteString(os.Stdout, ansiAltExit+"\r\n")
	}()

	_, _ = io.WriteString(os.Stdout, ansiAltEnter+ansiClearScreen)

	draw := func() {
		w, h, errSize := term.GetSize(outFd)
		if errSize != nil || w <= 0 || h <= 0 {
			w, h = 100, 28
		}
		frame := model.renderFrame(w, h)
		_, _ = io.WriteString(os.Stdout, frame)
	}
	draw()

	keyCh := make(chan []byte, 32)
	go func() {
		defer close(keyCh)
		buf := make([]byte, 64)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case keyCh <- cp:
				case <-ctx.Done():
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-model.notifyCh:
			draw()
		case <-ticker.C:
			draw()
		case raw, ok := <-keyCh:
			if !ok {
				return nil
			}
			if handleTUIKeyBytes(model, raw) {
				return nil
			}
			draw()
		}
	}
}

// handleTUIKeyBytes 解析终端原始按键序列（含方向键、PgUp/PgDn、Tab、退格、回车与 UTF-8 中文输入）。返回 true 表示退出 TUI。
func handleTUIKeyBytes(m *tuiModel, data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// 处理 ANSI 转义序列（方向键、PgUp/PgDn 等）
	if data[0] == 0x1b {
		seq := string(data)
		switch seq {
		case "\x1b[A": // Up
			m.mu.Lock()
			if len(m.inputBuf) == 0 && len(m.historyCmds) == 0 {
				m.mu.Unlock()
				m.cycleConversation(-1)
				return false
			}
			if len(m.historyCmds) > 0 && m.historyIdx > 0 {
				m.historyIdx--
				m.inputBuf = []rune(m.historyCmds[m.historyIdx])
			}
			m.mu.Unlock()
		case "\x1b[B": // Down
			m.mu.Lock()
			if len(m.inputBuf) == 0 && len(m.historyCmds) == 0 {
				m.mu.Unlock()
				m.cycleConversation(1)
				return false
			}
			if m.historyIdx < len(m.historyCmds)-1 {
				m.historyIdx++
				m.inputBuf = []rune(m.historyCmds[m.historyIdx])
			} else {
				m.historyIdx = len(m.historyCmds)
				m.inputBuf = nil
			}
			m.mu.Unlock()
		case "\x1b[5~": // PgUp
			m.mu.Lock()
			m.scrollOffset += 5
			m.mu.Unlock()
		case "\x1b[6~": // PgDn
			m.mu.Lock()
			m.scrollOffset -= 5
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
			m.mu.Unlock()
		}
		return false
	}

	for len(data) > 0 {
		b := data[0]
		switch b {
		case 3, 4: // Ctrl+C / Ctrl+D
			return true
		case '\t', 14: // Tab / Ctrl+N -> 下一会话
			m.cycleConversation(1)
			data = data[1:]
		case 16: // Ctrl+P -> 上一会话
			m.cycleConversation(-1)
			data = data[1:]
		case 21: // Ctrl+U -> 清空当前输入
			m.mu.Lock()
			m.inputBuf = nil
			m.mu.Unlock()
			data = data[1:]
		case '\r', '\n':
			m.mu.Lock()
			line := string(m.inputBuf)
			m.inputBuf = nil
			m.mu.Unlock()
			if m.executeTUICommand(line) {
				return true
			}
			data = data[1:]
		case 127, 8: // Backspace
			m.mu.Lock()
			if len(m.inputBuf) > 0 {
				m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
			}
			m.mu.Unlock()
			data = data[1:]
		default:
			r, sz := utf8.DecodeRune(data)
			if r != utf8.RuneError && r >= 0x20 {
				m.mu.Lock()
				m.inputBuf = append(m.inputBuf, r)
				m.mu.Unlock()
			}
			if sz <= 0 {
				sz = 1
			}
			data = data[sz:]
		}
	}
	return false
}
