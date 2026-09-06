# ADR 索引

> 架构决策记录（Architecture Decision Records）。每条记录 = 一次已拍板的架构决策。
> 新决策的模板见本文件底部；旧决策按编号收录。

| ADR | 决策（一句话） | 状态 | 文档 |
|---|---|---|---|
| ADR-002 | `pkg/core` 接口与 transport 实现解耦——一份逻辑、多种 transport | ✅ 已采纳 | [ADR-002.md](./ADR-002.md) |
| ADR-003 | 前端用 Go 全栈 templ + HTMX，不引入 JS 框架与构建工具 | ✅ 已采纳 | [ADR-003.md](./ADR-003.md) |
| ADR-008 | User/Device 二层身份，多设备同时在线不互踢，per-device 读游标 | ✅ 已采纳 | [ADR-008.md](./ADR-008.md) |
| ADR-009 | Client 内部管 `pumpCtx` 给 readPump，与 Connect 传入的 dialCtx 解耦 | ✅ 已采纳 | [ADR-009.md](./ADR-009.md) |
| ADR-010 | i18n 用 Translator interface 注入 + flat key + lowercase locale + JSON embed.FS | ✅ 已采纳 | [ADR-010.md](./ADR-010.md) |
| ADR-011 | Web 端用 SSE + POST /api/messages，不用 WebSocket | ✅ 已采纳 | [ADR-011.md](./ADR-011.md) |
| ADR-012 | Web 端 thin proxy：只做 HTTP ↔ pkg/client 桥接，不重复 hub 调度 | ✅ 已采纳 | [ADR-012.md](./ADR-012.md) |

## 新增 ADR 模板

```markdown
# ADR-XXX: <标题>

- 状态：✅ 已采纳 / 🟡 提议 / ⬜ 已废弃
- 日期：<yyyy-mm-dd>
- 关联：<相关 ADR / AGENTS.md 章节>

## 背景
<为什么需要这个决策；不决策的后果>

## 决策
<选了什么；核心约束与边界>

## 备选方案
<考虑过但否决的方案 + 否决理由>

## 落地位置
<代码路径 / commit 引用>

## 验证
<验收标准 / 测试 / 真机证据>
```

编号规则：按 AGENTS.md / 代码注释 / MEMORY.md 既有引用继续；编号不连续属正常（历史未成文的编号不再补）。
