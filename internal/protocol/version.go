// Package protocol 定义 LANChat 控制面（WebSocket 信令）与数据面
// （LCTP / P2P）的协议常量、消息类型与编解码。
//
// 本包只包含纯协议定义，不依赖任何业务逻辑。
package protocol

// ProtocolVersion 是当前控制面协议版本（语义化）。
// 与产品 SemVer 解耦，由人工 bump：
//   - 破坏性变更：升 major，并在提交 footer 标注 BREAKING CHANGE；
//   - 非破坏新增：升 minor。
const ProtocolVersion = "2.1"

// MinSupportedProtocol 是服务端仍接受的最低协议版本，
// 构成兼容区间 [MinSupportedProtocol, ProtocolVersion]（见方案 §18.5）。
const MinSupportedProtocol = "1.0"

// MaxControlPayload 是单条控制面消息的最大字节数（1 MiB）。
// 文件字节永远不进入控制面（控制面 / 数据面分离）。
const MaxControlPayload = 1 << 20

// HeartbeatIntervalSec 是默认心跳间隔（秒）。
const HeartbeatIntervalSec = 5
