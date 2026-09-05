// Package protocol 定义 lanchat 网络层与持久层的协议中立类型。
//
// 这里有两类东西：
//   - wire.go：在线缆上传的帧与编解码（Frame / FrameKind / Hello / Delivered 等）。
//   - message.go：业务域模型（StoredMessage / Conversation / User / Device / ReadCursor）。
//
// 这一包只放数据契约，**不允许有外部依赖**，方便其它任何模块引用。
package protocol

// ProtocolVersion 是线缆协议的当前版本。
// Hub 在收到不兼容的 Hello 时应返回 FKError 并关闭连接。
const ProtocolVersion uint8 = 1
