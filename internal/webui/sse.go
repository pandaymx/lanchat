package webui

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/pandaymx/lanchat/internal/webui/templates"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// writerBuf 是每条 SSE 连接在 Session 侧的帧缓冲长度。
//
// 满则丢该 writer 的帧（与 EventBus「满了就丢、发送端不阻塞」契约一致）：
// 一个卡住的慢 tab 不能拖累同 session 的其它 tab。
const writerBuf = 128

// sseWriter 是一条 SSE 连接在 Session fanout 里的登记。
// pump 把渲染好的帧写进 ch；/events handler 的事件循环读 ch 写 ResponseWriter。
type sseWriter struct {
	ch chan []byte
}

// addWriter 登记一条新 SSE 连接（多 tab 共享 session 时会有多个）。
func (s *Session) addWriter() *sseWriter {
	w := &sseWriter{ch: make(chan []byte, writerBuf)}
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
func (s *Session) broadcast(frame []byte) {
	s.mu.Lock()
	ws := make([]*sseWriter, 0, len(s.writers))
	for w := range s.writers {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		select {
		case w.ch <- frame:
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
				frame, ok := s.sseFrame(e)
				if !ok {
					continue
				}
				s.broadcast(frame)
			}
		}
	}()
}

// sseFrame 把一条 core.Event 翻译成完整 SSE 帧；ok=false 表示不产生帧。
//
// message 帧的 data 是渲染好的 <li> HTML 片段（templates.Message），按 SSE
// 规范逐行加 "data: " 前缀——templ 产物含换行（用户消息体可含换行），
// 整块塞一行会断帧；浏览器会把多个 data 行用 \n 拼回原文。
// id 行只在 ServerSeq > 0 时输出：EventSource 重连会带 Last-Event-ID 头，
// M4.6 可用它做断线补发。
//
// 渲染发生在 pump goroutine，用 Session 生命周期 ctx。
func (s *Session) sseFrame(e core.Event) ([]byte, bool) {
	switch e.Kind {
	case core.EventMessage:
		m := e.Message
		if m == nil || m.ConversationID != s.convID {
			return nil, false
		}
		var buf bytes.Buffer
		if err := templates.Message(s.newView(m)).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render message frame failed", "err", err)
			return nil, false
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
		return []byte(sb.String()), true

	case core.EventState:
		connected := e.State != nil && e.State.Connected
		// state 帧整段 swap 进 #conn-state：断连出 banner，重连渲染空内容清掉。
		var buf bytes.Buffer
		if err := templates.ConnStatus(connected).Render(s.ctx, &buf); err != nil {
			s.logger.Error("render state frame failed", "err", err)
			return nil, false
		}
		var sb strings.Builder
		sb.WriteString("event: state\n")
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			sb.WriteString("data: ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		return []byte(sb.String()), true

	default:
		// EventRead / EventPresence / EventTyping：M4 不渲染。
		return nil, false
	}
}

// newView 把协议消息转成视图模型。Self 以 SenderUser 与 session 身份比对。
func (s *Session) newView(m *protocol.StoredMessage) templates.MessageView {
	return templates.NewMessageView(m.ID, int64(m.ServerSeq), m.SenderUserID, m.Body, m.CreatedAt, m.SenderUserID == s.user)
}
