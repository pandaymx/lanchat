package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// FrameKind 区分帧语义。
//
// 帧按"方向角色"分两组：
//   - Client → Hub：Hello/Message/HistoryReq/Ack/Typing/Read  // CKxxx
//   - Hub → Client：Deliver/HistoryResp/Presence/Error/Pong   // HKxxx
//   - 双向：Ping
type FrameKind uint8

const (
	// FKHello 客户端首次连接握手（含 ResumeFrom）。
	FKHello FrameKind = iota + 1
	// FKMessage Client → Hub：我想发一条消息。
	FKMessage
	// FKDeliver Hub → Client：投递给你的消息（或者广播给参与者的）。
	FKDeliver
	// FKHistoryReq Client → Hub：请补发 ResumeFrom 之后的消息。
	FKHistoryReq
	// FKHistoryResp Hub → Client：补发结果（按 ServerSeq 升序）。
	FKHistoryResp
	// FKAck Client↔Hub：我已经收到了 ServerSeq<=S 的所有消息。
	FKAck
	// FKRead Client → Hub：标记某会话已读。
	FKRead
	// FKPresence Hub → Client：某设备/用户的上下线状态变化。
	FKPresence
	// FKTyping Client↔Hub：某设备正在输入。
	FKTyping
	// FKPing Client↔Hub：心跳探活。
	FKPing
	// FKPong 应答 FKPing。
	FKPong
	// FKError Hub → Client：错误通知（含关连接原因）。
	FKError
)

// String 实现 Stringer，仅用于日志和 CLI 输出，不参与 wire 协议。
func (k FrameKind) String() string {
	switch k {
	case FKHello:
		return "hello"
	case FKMessage:
		return "message"
	case FKDeliver:
		return "deliver"
	case FKHistoryReq:
		return "history_req"
	case FKHistoryResp:
		return "history_resp"
	case FKAck:
		return "ack"
	case FKRead:
		return "read"
	case FKPresence:
		return "presence"
	case FKTyping:
		return "typing"
	case FKPing:
		return "ping"
	case FKPong:
		return "pong"
	case FKError:
		return "error"
	default:
		return fmt.Sprintf("frame_kind(%d)", uint8(k))
	}
}

// Frame 是线缆上的最小传输单元。
//
// 编码策略（M1 简单起见，v1 wire 协议）：
//   - 长度前缀（4 字节大端 uint32，不含自身）
//   - 之后是 JSON 编码的 Frame
//
// 之所以用 JSON 而不是二进制：M1 只验证接口契约，wire format 简单且易调试。
// v2 可以无缝替换成 protobuf/capnproto，前提是 pkg/core 接口保持稳定。
type Frame struct {
	Kind FrameKind `json:"k"`
	// Ack 是发送方声明自己"已经收到 ≤ Ack 的所有消息"。Hub 据此决定是否给新连接补发。
	// 仅在客户端首帧 Hello 与后续 Ack 帧中使用，其它帧忽略。
	Ack uint64 `json:"a,omitempty"`
	// Payload 是该 Kind 对应的类型化负载的 JSON 编码。
	// 客户端/服务端按 Kind 解码，匹配规则见 wire.md（暂未生成文档，见各 kind 注释）。
	Payload []byte `json:"p,omitempty"`
}

// Hello 是 Client → Hub 的首帧，连接建立后第一件事就是发它。
type Hello struct {
	ProtocolVersion uint8  `json:"v"`
	DeviceID        string `json:"d"`           // 设备唯一 ID（UUID / 自建）
	UserID          string `json:"u"`           // 用户 ID
	Token           string `json:"t,omitempty"` // 鉴权令牌（M8 才会用）
	// ResumeFrom 是客户端希望 Hub 从哪个 ServerSeq 之后开始补发。
	// 首次连接填 0，断线重连时填 GetCursor(DeviceID, ConversationID) 返回的最大值。
	ResumeFrom uint64 `json:"r,omitempty"`
}

// Delivered 是一条被投递的消息的元数据，由 Hub 发给目标设备。
// 它和 StoredMessage 是同一结构体（参见 message.go），因为发给客户端的就是落库的形态。
type Delivered = StoredMessage

// HistoryRequest 用于补发请求。
type HistoryRequest struct {
	// ConversationIDs 为空表示查所有参与会话。
	ConversationIDs []string `json:"c,omitempty"`
	After           uint64   `json:"a"` // ServerSeq 必须严格大于此值才返回
	Limit           int      `json:"l,omitempty"`
}

// HistoryResponse 是补发的批量结果。
type HistoryResponse struct {
	Messages []StoredMessage `json:"m"`
	// HasMore 表示还有更多；客户端应再发一次 HistoryReq 续传。
	HasMore bool `json:"more"`
}

// Read 标记某设备在某会话已读到哪条 ServerSeq。
type Read struct {
	ConversationID string `json:"c"`
	ServerSeq      uint64 `json:"s"`
}

// Presence 是上下线状态变化。
type Presence struct {
	UserID   string `json:"u"`
	DeviceID string `json:"d,omitempty"`
	Online   bool   `json:"on"`
}

// ErrorPayload 是 FKError 帧的内容，描述为什么关连接或某次请求失败。
type ErrorPayload struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"m"`
}

// ErrorCode 是错误分类，与协议层 frame-level error 配合使用。
type ErrorCode uint16

const (
	// ErrProtocolMismatch 协议版本不兼容（用于 FKError 帧）。
	ErrProtocolMismatch ErrorCode = iota + 100
	// ErrUnauthorized 未通过认证（M8 引入鉴权时启用）。
	ErrUnauthorized
	// ErrRateLimited 触发速率限制。
	ErrRateLimited
	// ErrInvalidFrame 帧内容非法（解码失败、长度越界等）。
	ErrInvalidFrame
	// ErrInternal Hub 内部错误。
	ErrInternal
)

// ----- IO 边：Frame 编码/解码 -----

// EncodeFrame 把 Frame 写入 w：4 字节长度前缀 + JSON body。
func EncodeFrame(w io.Writer, f Frame) error {
	body, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// DecodeFrame 从 r 读取一个 Frame（长度前缀 + JSON body）。
// 限制 body 不超过 MaxFrameBodyBytes，避免恶意客户端发送巨型帧拖死内存。
func DecodeFrame(r io.Reader) (Frame, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Frame{}, err // 包含 io.EOF，调用方据此判断连接关闭
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 {
		return Frame{}, ErrBadFrameLength
	}
	if n > MaxFrameBodyBytes {
		return Frame{}, ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(body, &f); err != nil {
		return Frame{}, fmt.Errorf("decode frame json: %w", err)
	}
	return f, nil
}

// MaxFrameBodyBytes 单帧 body 最大字节数（1 MiB）。超出会被 DecodeFrame 拒绝。
const MaxFrameBodyBytes = 1 << 20

// ErrFrameTooLarge 在 body 超过 MaxFrameBodyBytes 时返回。
var ErrFrameTooLarge = errors.New("frame body too large")

// ErrBadFrameLength 当长度前缀非法（如超过限制）时返回。
var ErrBadFrameLength = errors.New("bad frame length")
