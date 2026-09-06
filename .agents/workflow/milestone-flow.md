# 里程碑推进 SOP（Agent 工作流）

> 用户工作方式：**方案优先、按需生成**。默认只产出方案与文档，等用户说「生成 / 搭建 / 开始做」再写实现代码。

## 标准流程

1. **读上下文**：`AGENTS.md` 当前阶段 + `.agents/rules/vision.md`（愿景与边界）
2. **摸底**：verify-don't-assume —— 用 grep / git log / 实际读文件证实现状，不凭印象断言
3. **方案优先**：proposal 落盘 `docs/proposals/<date>-<topic>-proposal.md`（不入库，`.git/info/exclude` 本地排除）
4. **用户拍板**：等用户选定方案后再进入实现
5. **原子提交**：按 AGENTS.md §5.2 三问拆 commit（一句话说清？可单独 revert？无无关文件？）
6. **验证矩阵**：见下节，全绿才提交/推送
7. **文档同步**：AGENTS.md + README.md 同步实际进度（长文档滞后是真实反馈，用户会提醒）
8. **发版**：semantic-release 自动管理，不手动改版本号 / 打 tag

## 验证矩阵（默认全量）

| 检查 | 命令 | 说明 |
|---|---|---|
| 单测（race） | `go test -race -count=1 ./...` | 全绿 |
| 静态检查 | `golangci-lint run ./...` | 0 issues |
| vet | `go vet ./...` | 0 输出 |
| 模板生成 | `make templ`（改了 `.templ` 必跑） | CI 会先 generate 再 build |
| 真机冒烟 | 对应 cmd 起服务实测 | 浏览器/TUI 实际看渲染 |

## 已知 flaky / 环境坑防御（避免重复踩）

- **WS 测试 dial 后必须 `awaitReady`**：FKPing→FKPong 往返屏障，保证 Hub 已处理 application-level hello（race+cover 下 FKHello 异步竞争会漏广播）
- **SSE 测试 teardown 死锁**：`t.Cleanup(srv.Close)` 先注册、cancel/body-close 后注册（Cleanup 逆序执行），封装 `startTestSSE` helper
- **测试命令不用管道**：`go test ... | tail` 退出码来自 tail，FAIL 也显示成功；必须 `set -o pipefail`
- **`<templ>` 版本 pin 一致性**：go.mod / CI / Makefile 三处 `templ@v0.3.1020` 必须一致
- **编辑器缓冲会还原 agent 的文件编辑**：关键改动用 Write 整文件落盘或 python 直接改磁盘，改完 grep / go vet 验证
- **gci sections 参数**：`--no-lex-order -s standard -s default -s "prefix(github.com/pandaymx/lanchat)"`，否则全仓 import 分组被重排

## 提交规范速查

- `type(scope): subject`，scope 白名单：`core proto hub tui web desktop mobile store deps ci docs repo release`
- subject ≤72 字符，祈使句，不加句号；不加 Co-Authored-By 自动签名
