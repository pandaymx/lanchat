package webui

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// writerBuf 是每条 SSE 连接在 Session 侧的帧缓冲长度。
//
// 满则丢该 writer 的帧（与 EventBus「满了就丢、发送端不阻塞」契约一致）：
// 一个卡住的慢 tab 不能拖累同 session 的其它 tab。
const writerBuf = 128

// sseChunk 是 fanout 通道里的一帧。
//
// seq 是消息帧的 ServerSeq（state 等非消息帧为 0），供重连补发后的
// 幂等去重：事件循环里 seq <= writer.skipSeq 的帧直接丢弃（该消息
// 已由 catchUp 直接补写给本连接）。
type sseChunk struct {
	seq  uint64
	data []byte
}

// sseWriter 是一条 SSE 连接在 Session fanout 里的登记。
// pump 把渲染好的帧写进 ch；/events handler 的事件循环读 ch 写 ResponseWriter。
//
// skipSeq 是重连补发阈值：catchUp 直接补写过的消息序号上界，
// fanout 缓冲里同序号的帧在事件循环里跳过，保证重连不重复、不丢失。
type sseWriter struct {
	ch      chan sseChunk
	skipSeq atomic.Uint64
}

// addWriter 登记一条新 SSE 连接（多 tab 共享 session 时会有多个）。
func (s *Session) addWriter() *sseWriter {
	w := &sseWriter{ch: make(chan sseChunk, writerBuf)}
	s.mu.Lock()
	s.writers[w] = struct{}{}
	s.mu.Unlock()
	return w
}

// removeWriter 注销一条 SSE 连接并关闭其帧 channel。
//
// 幂等：session 死亡时 closeWriters 可能已摘除并 close 过，这里找不到
// 就不动，避免重复 close channel。
func (s *Session) removeWriter(w *sseWriter) {
	s.mu.Lock()
	if _, ok := s.writers[w]; ok {
		delete(s.writers, w)
		close(w.ch)
	}
	s.mu.Unlock()
}

// closeWriters 注销全部 writer（session 关闭时调）：各 SSE handler 读到
// channel 关闭后结束响应流，浏览器 EventSource 自动重连。
func (s *Session) closeWriters() {
	s.mu.Lock()
	for w := range s.writers {
		close(w.ch)
	}
	s.writers = nil
	s.mu.Unlock()
}

// broadcast 把一帧投递给所有当前 writer；慢 writer（ch 满）丢帧不阻塞。
// seq 为消息帧的 ServerSeq（非消息帧传 0），随帧带上供重连去重。
func (s *Session) broadcast(seq uint64, frame []byte) {
	s.mu.Lock()
	ws := make([]*sseWriter, 0, len(s.writers))
	for w := range s.writers {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		select {
		case w.ch <- sseChunk{seq: seq, data: frame}:
		default:
			s.logger.Warn("sse writer slow, frame dropped", "cookie", s.id)
		}
	}
}

// startPump 启动 Session 的唯一事件泵：一条 EventBus 订阅 → 事件渲染成
// SSE 帧（只渲染一次）→ fanout 给所有 SSE 连接。多 tab 共享靠这一条。
//
// 退出条件：cli.Done()（hub 连接断开）。退出时 markDead + closeWriters，
// 各 SSE 流结束并由浏览器自动重连；Manager 侧死 session 由下次
// GetOrCreate 重建或 janitor 清扫回收。
func (s *Session) startPump() {
	sub := s.cli.Subscribe(eventBuf)
	go func() {
		defer func() { _ = sub.Close() }()
		for {
			select {
			case <-s.cli.Done():
				s.markDead()
				s.closeWriters()
				s.logger.Info("event pump stopped: hub connection lost", "cookie", s.id)
				return
			case e := <-sub.C():
				seq, frame, ok := s.sseFrame(e)
				if !ok {
					continue
				}
				s.broadcast(seq, frame)
			}
		}
	}()
}

// catchUp 是断线重连补发：把本地 Store 中 seq 严格大于 lastID 的消息
// 作为 message 帧直接写给本条 SSE 连接，并把补发上界记到 writer——
// 补发窗口内已 fanout 进通道缓冲的同序号帧会在事件循环里被跳过，
// 因此与实时帧天然幂等（不重复、不丢失）。
//
// 数据源是 client 本地 Store（Connect 的 catch-up 已拉过最近历史）；
// lastID 太老、超出本地窗口的缺口补不到，刷新整页即可恢复（M4 可接受）。
func (s *Session) catchUp(w http.ResponseWriter, flusher http.Flusher, wr *sseWriter, lastID uint64) {
	msgs, err := s.cli.History(s.ctx, s.convID, lastID, historyLimit)
	if err != nil {
		s.logger.Warn("catch-up history failed", "cookie", s.id, "after", lastID, "err", err)
		return
	}
	var maxSeq uint64
	for i := range msgs {
		seq, frame, ok := s.sseFrame(core.Event{Kind: core.EventMessage, Message: &msgs[i]})
		if !ok {
			continue
		}
		if _, err := w.Write(frame); err != nil {
			s.logger.Info("catch-up write failed, dropping client", "cookie", s.id, "err", err)
			return
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	if maxSeq > 0 {
		flusher.Flush()
		// 阈值在补发帧全部写出后设置：此后事件循环里 seq<=maxSeq 的
		// fanout 帧（补发期间入缓冲的）一律跳过。
		wr.skipSeq.Store(maxSeq)
		s.logger.Debug("catch-up replayed", "cookie", s.id, "after", lastID, "count", len(msgs))
	}
}

// sseFrame 把一条 core.Event 翻译成完整 SSE 帧。
//
// 返回值 seq 为消息帧的 ServerSeq（非消息帧为 0），随帧进 fanout 通道
// 供重连去重；ok=false 表示不产生帧。
//
// message 帧的 data 是渲染好的 <li> HTML 片段（templates.Message），按 SSE
// 规范逐行加 "data: " 前缀——templ 产物含换行（用户消息体可含换行），
// 整块塞一行会断帧；浏览器会把多个 data 行用 \n 拼回原文。
// id 行只在 ServerSeq > 0 时输出：EventSource 重连会带 Last-Event-ID 头，
// handleEvents 用它做断线补发（见 catchUp）。
//
// 渲染发生在 pump goroutine，用 Session 生命周期 ctx。
func (s *Session) sseFrame(e core.Event) (uint64, []byte, bool) {
	switch e.Kind {
	case core.EventMessage:
		m := e.Message
		if m == nil || m.ConversationID != s.convID {
			return 0, nil, false
		}
		var buf bytes.Buffer
		if err := templates.Message(s.newView(m)).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render message frame failed", "err", err)
			return 0, nil, false
		}
		var sb strings.Builder
		sb.WriteString("event: message\n")
		if m.ServerSeq > 0 {
			sb.WriteString("id: ")
			sb.WriteString(strconv.FormatUint(m.ServerSeq, 10))
			sb.WriteString("\n")
		}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			sb.WriteString("data: ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		return m.ServerSeq, []byte(sb.String()), true

	case core.EventState:
		connected := e.State != nil && e.State.Connected
		// state 帧整段 swap 进 #conn-state：断连出 banner，重连渲染空内容清掉。
		var buf bytes.Buffer
		if err := templates.ConnStatus(s.tr, connected).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render state frame failed", "err", err)
			return 0, nil, false
		}
		return 0, sseDataFrame("state", buf.Bytes()), true

	case core.EventPresence:
		// M7.2：成员上下线。事件到达时 client 内部名单已更新（dispatch
		// 先 applyPresence 后发布事件），取全量快照整段重渲 #peers。
		peers := templates.NewPeerViews(s.cli.Peers(), s.device)
		var buf bytes.Buffer
		if err := templates.Peers(s.tr, peers).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render presence frame failed", "err", err)
			return 0, nil, false
		}
		return 0, sseDataFrame("presence", buf.Bytes()), true

	case core.EventTyping:
		// M7.3：有人正在输入。事件到达时 client 的 typing 快照已更新
		// （dispatch 先 applyTyping 后发布事件），取全量快照整段重渲
		// #typing。片段自带 6s 后 GET /typing 自刷新——停止输入后没有
		// 新事件，靠这次刷新取到空快照把指示条清掉；持续输入时新帧
		// 不断 swap，定时器随之续期。
		var buf bytes.Buffer
		if err := templates.TypingBar(s.tr, templates.NewTypingViews(s.cli.Typing())).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render typing frame failed", "err", err)
			return 0, nil, false
		}
		return 0, sseDataFrame("typing", buf.Bytes()), true

	default:
		// EventRead：M7 仍不渲染。
		return 0, nil, false
	}
}

// sseDataFrame 把渲染好的 HTML 片段包成具名 SSE 事件帧。
//
// templ 产物含换行（消息体/模板缩进都可能产生），整块塞一行会断帧；
// 按 SSE 规范逐行加 "data: " 前缀，浏览器把多个 data 行用 \n 拼回原文。
// 不带 id 行——state/presence 是瞬时状态帧，无需重连续传（重连后首屏
// 服务端渲染 + client 快照自然是最新的）。
func sseDataFrame(event string, html []byte) []byte {
	var sb strings.Builder
	sb.WriteString("event: ")
	sb.WriteString(event)
	sb.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(string(html), "\n"), "\n") {
		sb.WriteString("data: ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return []byte(sb.String())
}

// newView 把协议消息转成视图模型。Self 以 SenderUser 与 session 身份比对。
func (s *Session) newView(m *protocol.StoredMessage) templates.MessageView {
	return templates.NewMessageView(m.ID, int64(m.ServerSeq), m.SenderUserID, m.Body, m.CreatedAt, m.SenderUserID == s.user)
}
