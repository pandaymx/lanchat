// Package group 实现 M9 群组广播：G1 全体广播（#all）与 G2 自定义频道。
//
// 设计边界（对齐 AGENTS.md §2 责任田与方案 §9）：
//
//   - 本包是群组语义与编排的唯一落点，不依赖 internal/core、server、transfer；
//     与外层之间仅通过接口协作（BlockSource 取块、EventSink 上抛事件），
//     数据面的具体适配（P2P/中继）由外层在接线阶段注入。
//   - 文件复用 transfer 的 4 MiB block 切分；块交换走协议预留的
//     BITFIELD(0x10)/REQUEST(0x11)/BLOCK(0x12)/HAVE(0x13) 四帧。
//   - 启用门槛（方案 §9.4）：接收方 N≥2 才走 swarm；N=1 退化为单份
//     单播（Complete 时 N=1）；并发种子 ≤ min(3, ceil(N/2))。
//   - 消息与频道均不持久化：离线成员不补历史，重新加入后只见新消息。
package group
