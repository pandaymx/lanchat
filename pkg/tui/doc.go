// Package tui 提供基于 Charm bubbletea/v2 的终端交互式聊天客户端。
//
// 设计要点（与 M3 方案对齐，ADR 见 docs/decisions/）：
//
//  1. 单一 goroutine 的 Elm 架构：
//     Model 不持有 *Client.Client，所有外部事件通过 channel 投递给
//     bubbletea 的 Program（Update），由 Update 在事件循环中统一处理，
//     避免与 Client 的内部读循环产生数据竞争。
//
//  2. 副作用通过 tea.Cmd 表达：
//     发送消息、订阅事件、退出等均返回 tea.Cmd，由 bubbletea 调度。
//     Model 本身保持纯函数式的 Init/Update/View。
//
//  3. UI 区域划分：
//     - 顶部 status 状态栏（连接状态 / 在线设备 / 未读计数）
//     - 中部 history viewport（消息流）
//     - 右侧 sidebar（presence 在线设备列表面板，可折叠）
//     - 底部 textarea（消息输入，Enter 发送 / Shift+Enter 换行）
//
// 4. 终端宽高自适应：
// WindowSizeMsg 由 bubbletea 自动投递，Update 据此调整 viewport/textarea
// 宽度，避免硬编码 80 列导致的文本溢出或高度断层。
//
// 5. 依赖下沉：
// 本包对外仅依赖 stdlib + charm.land/{bubbletea,lipgloss,bubbles}/v2 +
// 本项目 pkg/client + pkg/proto。绝不直接 import cmd/* 或 internal/*。
package tui
