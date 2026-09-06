# 代码审查清单（多 Agent 互审）

> 适用：agent 的改动由另一个 agent 审查；或提交前自审。
> 原则：审查者**只读代码与测试**，用 grep / git log 证实结论（verify-don't-assume），不凭印象。

## 1. 范围与原子性

- [ ] 一个 commit 只做一件事，可独立 revert（AGENTS.md §5.2 三问）
- [ ] 无调试残留（`git grep -nP 'fmt\.Println|Fprintf\(os\.Stderr'` 自查）
- [ ] 生成物不入库（`*_templ.go` / `bin/` / `*.db`）
- [ ] subject ≤72 字符、scope 在白名单、commitlint 过

## 2. 架构铁律（AGENTS.md §3）

- [ ] `pkg/core` 未 import 任何 UI 库或平台 API
- [ ] 业务层只认 `Transport` 与 `Store` 接口，不认 WebSocket/SQLite/IP
- [ ] 消息排序与同步以服务端 `server_seq` 为准，无客户端时钟排序
- [ ] 版本号未写入源码（ldflags 注入）
- [ ] 无新 CGO 依赖 / 无 JS 框架 / 无 ORM（AGENTS.md §2 禁区）

## 3. 多端视角（`.agents/rules/multi-device.md`）

- [ ] 未引入 user 级游标替代 per-device 游标
- [ ] 新端/新功能复用 `pkg/core` + `pkg/client`，未另起炉灶
- [ ] 新功能附「同一 user 双设备同时在线」验收用例
- [ ] 消息投递到该 user 所有在线设备（未被改成互踢/单设备）

## 4. 生命周期与并发

- [ ] 长生命周期 goroutine 用自己的 ctx（`context.WithCancel(Background())`），不继承外层短期 ctx（ADR-009）
- [ ] `defer cancel()` 不影响被分离的 goroutine（对照 pumpCtx 模式）
- [ ] 共享状态有锁 / channel 串行化，无裸并发写

## 5. 错误与日志

- [ ] 所有 `return err` 前有 `log.Error`（`pkg/logging` 门面，不直接 import slog）
- [ ] 关键路径（connect/disconnect、send/receive、broadcast、catch-up）有埋点
- [ ] 高频循环只走 Debug 级别

## 6. 测试与验证

- [ ] 新代码有测试；关键路径有回归测试（尤其 catch-up / 多设备 / 断线重连）
- [ ] `go test -race -count=1 ./...` 全绿
- [ ] `golangci-lint run ./...` 0 issues；`go vet ./...` 干净
- [ ] `.agents/tools/check.sh` 7 项全 PASS（pre-commit 已自动跑）
- [ ] 涉及模板改动跑了 `make templ`；真机冒烟覆盖本次改动路径

## 7. 交接（`.agents/workflow/handoff.md`）

- [ ] 当日日志更新了「完成项 / 待办 / 工作树状态」
- [ ] 中间产物路径写入日志，未散落仓库根
