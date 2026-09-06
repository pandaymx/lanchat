# 会话交接模板（Agent 必读）

> 目标：任何时刻中断 / 换 agent，下一个 agent **5 分钟内进入状态**。
> 产出物：每次会话结束更新 `.agents/history/<yyyy-mm-dd>.md`（当日日志），按本模板结构组织。
> 长期记忆在 `.agents/history/MEMORY.md`，每日工作记录在当日日志，两者分工不重复。

## 会话开始 checklist（新 agent 进入时）

1. 读 `AGENTS.md`「当前阶段」+ `.agents/rules/vision.md`（愿景与边界）
2. 读 `.agents/history/MEMORY.md`（ADR / 里程碑 / 持续关注 / 待办）
3. 读最近的 1-2 份当日日志（最近进度与踩坑）
4. 摸工作树状态：`git status --short`、`git worktree list`（可能有他人/上次会话的 in-progress 改动）
5. 跑 `.agents/tools/check.sh` 确认仓库一致性健康
6. 定位待办：MEMORY.md「待办（跨会话）」或最近日志「待办」段，确认当前做到哪一步

## 当日日志标准结构（会话结束必须更新）

| 段 | 内容 | 示例 |
|---|---|---|
| 1. 主题 | 本次会话一句话 | `M4.4 多 Tab Session：cookie 签发落地` |
| 2. 完成项 | commit hash + 一句说明（表格） | `abc1234 feat(web): Session.Manager cookie 签发` |
| 3. 踩坑 | 值得记的坑：现象 → 根因 → 正解 | 见 MEMORY.md「持续关注」风格 |
| 4. 验证 | 跑了什么、结果 | `go test -race ./... 全绿` |
| 5. 待办 | 下一步 + 未完成的中断点 | `M4.5 输入体验，输入框 Enter 提交未做` |
| 6. 工作树状态 | main 是否有未提交改动、worktree 列表、中间产物路径 | `main 有 M webui/* 未提交；产物在 .agents/scratchpad/2026-09-06-m4.4/` |

## 会话结束 checklist

1. 提交 / 合并完成？未完成的部分写明**中断点**（做到哪、卡在哪），不靠口头记忆
2. 中间产物路径写入当日日志「工作树状态」段（遵守 scratchpad 并发约定：自己的子目录）
3. 工作树是否 clean？遗留的未提交改动**注明归属**（谁改的、为什么留着）
4. 值得沉淀的坑同步进 MEMORY.md「持续关注」（当日日志偏流水，MEMORY 是检索入口）

## 铁律

- 当日日志**必须**每天更新；中断（超时 / 换会话）前至少写「待办」与「工作树状态」两段
- 不把「上次做到哪」留在自己脑子里——多 AI Agent 场景下，记忆只在文件里
