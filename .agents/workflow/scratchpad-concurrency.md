# 多 Agent 并发工作约定

> 适用：多个 AI Agent（或你 + Agent）同时在 lanchat 仓库干活。
> 目的：防止临时文件互踩、未提交改动互相覆盖、git status 被中间产物污染。
> 违反代价低但烦人（丢草稿 / 误提交 / 冲突重来），值得按约定执行。

## 1. scratchpad：一个任务一个子目录

- `.agents/scratchpad/<task-id>/` 一个任务一个子目录，**禁止**直接写 `.agents/scratchpad/` 根
- task-id 命名：`<yyyy-mm-dd>-<短描述>`，如 `2026-09-06-m4.4-session`、`2026-09-06-check.sh`
- 他人的子目录**只读**：不写、不删、不覆盖
- 会话结束不清理（保留排查线索）；确需清理只清自己的子目录

## 2. 工作树：一个任务一个 worktree

- 涉及代码 / 文档改动，默认 `git worktree add -b <branch> ../lanchat-<短名>`，**不在 main 工作树里攒未提交改动**
- 一个 worktree 只服务一个任务；任务完成后 `git merge --ff-only <branch>` 线性合并 → `worktree remove` → `branch -d`
- 多任务并行 → 多个 worktree 互不干扰
- 临时中间产物放 scratchpad 子目录，不放仓库根（避免污染 git status 与误提交）

## 3. 改动边界

- 只改自己任务的 worktree / scratchpad 子目录；他人 worktree 的未提交改动一律不动
- 需要借用他人中间产物时：只读复制到自己的子目录，不原位修改
- 合并前确认目标分支工作树 clean（`git status --short` 为空），有本地改动先 stash / 提交

## 4. 交接信号

- commit message 里写清本次改动的产物路径（便于下一个 agent 定位）
- 未完成任务的中间产物路径写入 `.agents/history/` 当日日志或会话交接，不靠口头记忆
