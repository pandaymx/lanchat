# AGENTS.md

> 本文件是给 AI 编码助手（Claude / CodeBuddy / Cursor 等）的工程约定。
> **动手改代码前先读完。** 与口语指令冲突时，以本文件为准——除非用户明确说「这次按我说的改」。

---

## 1. 项目是什么

**LAN Chat** —— 局域网即时通讯，Go 为主，面向程序员用户，覆盖 TUI / Web / 桌面 / 移动四端。

**MVP 判据（一句话）**：一个程序员在局域网里，用两个终端窗口，能可靠地把一段代码发给同事；关掉重开消息还在；断网重连能补回漏掉的消息。

**当前阶段**：M0 工程地基（尚未写代码）。完整方案见 `方案.md`。

---

## 2. 技术栈（硬性约束，不要提议替换）

| 项 | 选择 | 约束 |
|---|---|---|
| 语言 | Go 1.27 | |
| 前端 | templ + HTMX | **不引入 React/Vue/任何 JS 框架，不引入前端构建工具** |
| 传输 | WebSocket（一期唯一实现） | 必须走 `Transport` 接口 |
| 存储 | `modernc.org/sqlite` | **纯 Go 无 CGO** |
| 任务入口 | Makefile（Go 侧）/ package.json（Node 侧） | 不用 bun 包办 Go 命令 |
| Node 运行时 | bun 1.3.14 | **只用于 commitlint 与 semantic-release** |

**禁止引入**：任何需要 CGO 的依赖、JS 前端框架、ORM（手写 SQL）、除标准库 `log/slog` 外的日志库。

**关键库的选型理由**：WebSocket 用 `coder/websocket`（gorilla 已归档）；SQLite 用 `modernc.org/sqlite`（`mattn/go-sqlite3` 需 CGO，会毁掉交叉编译与 gomobile）。

---

## 3. 架构铁律（违反会被打回）

1. **`pkg/core` 不得 import 任何 UI 库或平台 API**。文件路径、通知、剪贴板一律通过接口注入。
2. **业务层只认 `Transport` 与 `Store` 两个接口**，不认 WebSocket、不认 SQLite、不认 IP 地址。
3. **换传输架构 = 换一个 `Transport` 实现，`pkg/core` 零行修改**。这是 ADR-002 的验收标准，不是美好愿望。
4. **消息排序与同步一律以服务端 `server_seq` 为准**，禁止依赖客户端时钟做排序。
5. **版本号绝不写入源码**，构建时用 ldflags 注入（见 §6）。
6. **同步游标是 per-device 的**（`read_cursors` 表），不是 per-user。见 §7。

---

## 4. 目录结构

```
lanchat/
├── pkg/core/          # ★ 跨端共享内核，禁止依赖任何 UI/平台 API
│   ├── client.go      #   Client 接口
│   ├── transport.go   #   Transport 接口（换架构的开关点）
│   ├── store.go       #   Store 接口
│   ├── event.go       #   事件总线
│   ├── model/         #   User / Device / Conversation / Message
│   ├── protocol/      #   协议信封与 Op 常量（纯数据，无依赖）
│   └── sync.go        #   离线补发
├── internal/
│   ├── hub/           # 服务端：连接管理、路由、广播、鉴权
│   ├── webui/         # Web 端：templ 模板 + 静态资源
│   ├── tui/           # 终端 UI：Bubble Tea
│   └── discovery/     # mDNS 广播与发现
├── cmd/
│   ├── hub/           # 服务端入口
│   ├── tui/           # 终端客户端入口
│   └── web/           # Web 服务端入口
├── apps/              # 二期：desktop(Wails) / mobile(gomobile+Flutter)
└── test/integration/  # 多端并发对发
```

`pkg/` 可被外部 import（gomobile bind 需要）；`internal/` 不行。放错位置会导致移动端无法复用。

---

## 5. 提交规范

### 5.1 Conventional Commits + scope 白名单

```
<type>(<scope>): <subject>
```

**type**：`feat` `fix` `refactor` `perf` `test` `docs` `build` `ci` `chore` `revert`

**scope 只能是**：`core` `proto` `hub` `tui` `web` `desktop` `mobile` `store` `deps` `ci` `docs` `repo` `release`

**subject**：≤72 字符，祈使句，不加句号。

```
✅ feat(core): 定义 Transport 接口与内存 FakeTransport
✅ fix(tui): 修复消息列表滚动到底部后跳回顶部
✅ chore(repo): 接入 lefthook 与 golangci-lint
❌ 更新代码
❌ feat: 加了一堆东西
❌ feat(Core): Add transport interface     （英文、大写、scop 拼错）
```

scope 强制白名单不是形式主义——它逼着每次提交想清楚「这次改的是哪一层」，多端仓库的可追溯性全靠它。

### 5.2 原子化提交（硬性要求）

**定义**：一个 commit = 一个可独立理解、可独立回滚、可独立 cherry-pick 的变更单元。

| 规则 | 反例 |
|---|---|
| 一次只做一件事，不混 feat + refactor | `feat(core): 加同步逻辑并重构日志` |
| 必须编译通过（pre-commit 跑 `go build`） | 提交半成品 |
| 必须测试通过（pre-commit 跑 `go test`） | 「测试先跳过，下个 commit 补」 |
| 不留调试残留（无 Println / 无注释掉的代码块） | 提交里带 print 调试 |
| 生成物不入库（`*_templ.go`、`bin/`、`*.db` 已 gitignore） | 把编译产物提交 |
| **迁移与逻辑分离**：改表结构一个 commit，用新结构的业务逻辑下一个 commit | 一次性提交，回滚时炸掉 |

**提交前自检（三个问题）**：
1. subject 能不能一句话说清？说不清 → 拆
2. 出问题能单独 revert 这个 commit 吗？不能 → 拆
3. 里面有没有与 subject 无关的文件？有 → 拿出来（用 `git add -p` 分块暂存）

---

## 6. 版本与发布

- 用 **semantic-release** 自动管理：分析 commit → 定版本号 → 生成 CHANGELOG → commit → 打 tag
- **不发版时不要手动改版本号，也不要手动打 tag**
- **版本号不写入任何 Go 源码**。构建注入方式：

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
```

所以 `lanchat --version` 输出的版本来自 git tag，源码里搜不到版本号字符串。
release 阶段只提交 `CHANGELOG.md`，**不要顺手改任何 .go 文件**。

发布命令（手动触发，不接 CI 自动发布）：

```bash
bun run release:dry     # 先 dry-run 确认算出的版本号与 notes
bun run release         # 正式发版
```

---

## 7. 多设备模型（ADR-008）

**身份分两层**：`User`（人）与 `Device`（设备）。一个 User 可有多个 Device 同时在线，消息投递到该 User 的**所有**在线 Device（不是互踢）。

**per-device 游标是硬要求**：

```
❌ conversations.last_seq          （用户级游标 → 第二台设备会漏消息或重复拉全量）
✅ read_cursors(device_id, conv_id, last_seq)
```

一期（MVP）一个 Device 即一个身份，结构上支持一对多但 UI 不暴露；二期做配对码绑定、全设备同步、设备列表管理。

---

## 8. 环境

- Arch Linux（WSL2），用户 `ppmb`，shell `zsh`
- Go 1.27.0；bun 在 `~/.bun/bin`（**不在默认 PATH**，命令跑不通先检查这个）
- `.wslconfig` 已是 `networkingMode=Mirrored` → 局域网设备可直连 WSL 里的服务，mDNS 可用
- 项目在 `~/code/lanchat`（ext4），**不要放到 `/mnt/c`**（9P 文件系统，构建慢且 inotify 失效）

---

## 9. 命令速查

```bash
make build       # 构建全部 cmd（ldflags 注入版本）
make test        # go test -race ./...
make lint        # golangci-lint run
make fmt         # gofumpt + gci + templ fmt
make templ       # templ generate（改了 .templ 必须跑）
make run-hub     # 跑服务端

bun run commitlint   # 校验提交信息
bun run release:dry  # 发布预演
lefthook install     # 装 git 钩子（clone 后必做）
```

---

## 10. 改动流程

1. 先读 `方案.md` 确认当前里程碑与验收标准
2. 改代码 → `make fmt` → `make lint` → `make test`
3. 用 `git add -p` 分块暂存，按 §5.2 拆成原子提交
4. 提交信息按 §5.1 写
5. **不要**在 commit message 里加 `Co-Authored-By` 之类的自动签名，除非用户要求

## 11. 不确定时

- 架构层面的取舍 → 先更新 `方案.md`，**不要直接写代码**
- 本文件有歧义或过时 → 直接改本文件，并在 commit message 里说明
- 用户没明确要求的重构/优化 → 不做（偏好最小改动）
