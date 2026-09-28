package protocol

import (
	"encoding/json"
	"time"
)

// Envelope 是所有控制面（WebSocket）消息的统一信封（方案 §3.1）。
type Envelope struct {
	// V 为信封/协议版本（取自 WELCOME.protocolVersion）。
	V int `json:"v"`
	// Type 为消息类型，取值见 message.go 中的常量。
	Type string `json:"type"`
	// ID 为消息唯一标识（UUIDv4），用于关联 ACK 与去重。
	ID string `json:"id"`
	// From 为发送者 user ID；服务器下发时填写，客户端上行可省略。
	From string `json:"from,omitempty"`
	// To 为接收者 user ID；空表示广播 / 发往服务器。
	To string `json:"to,omitempty"`
	// Group 为群组范围："" = 单播，"*" = 全体（G1），其他 = G2 频道 ID。
	Group string `json:"group,omitempty"`
	// Seq 为会话内单调递增序号，用于 ACK 与排序。
	Seq uint64 `json:"seq,omitempty"`
	// ReplyTo 关联所回复的原消息 ID。
	ReplyTo string `json:"replyTo,omitempty"`
	// TS 为发送时间（Unix 毫秒）。
	TS int64 `json:"ts"`
	// Payload 为类型相关的负载，延迟到按 Type 解码。
	Payload json.RawMessage `json:"payload,omitempty"`
}

// 群组范围常量（方案 §3.1 / §9.1）。
const (
	// GroupUnicast 单播（无群组）。
	GroupUnicast = ""
	// GroupBroadcast 全体广播（G1）。
	GroupBroadcast = "*"
)

// NewEnvelope 以给定类型与负载构造信封，自动填充当前协议版本与时间戳。
// payload 为 nil 时不带负载。
func NewEnvelope(id, typ string, payload any) (*Envelope, error) {
	env := &Envelope{
		V:    envelopeV,
		Type: typ,
		ID:   id,
		TS:   time.Now().UnixMilli(),
	}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		env.Payload = raw
	}
	return env, nil
}

// DecodePayload 将信封负载解码到 v。
func (e *Envelope) DecodePayload(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// Marshal 编码信封为 JSON。
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope 从 JSON 解码信封。
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
