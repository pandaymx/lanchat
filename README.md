# LAN Chat

局域网即时通讯，Go 优先，面向程序员用户，覆盖 TUI / Web / 桌面 / 移动四端。

> 当前状态：**M3 TUI 客户端 M3.1–M3.9 全部完成**，待打 v0.3.0 tag。下一里程碑 M4 Web 端。
> 完整里程碑定义与子任务拆解见 [`AGENTS.md`](./AGENTS.md) §11。

## 架构（极简版）

```
┌───────────────┐         ┌─────────────────┐         ┌───────────────┐
│  pkg/client   │  core   │    pkg/hub      │  proto  │ pkg/transport │
│  (任何端通用) ├────────▶│  Router+Registry├────────▶│  ws / fake    │
└───────────────┘         │  History+Peer   │         └───────────────┘
        ▲                 └─────────────────┘
        │ EventBus                 ▲
┌───────┴───────┐                 │
│  pkg/tui 端   │                 │ ws
│ (bubbletea)   │◀────────────────┘
└───────────────┘
```

- **`pkg/core`**：四端共享接口层（`Client / EventBus / Conn / Store`），由 `pkg/transport` 提供实现
- **`pkg/hub`**：服务端核心，`Router+Registry+History+Peer` 组成，详见 `pkg/hubstate`
- **`pkg/tui`**：bubbletea v2 终端客户端，只做 View 层：订阅 `pkg/client.EventBus` → 渲染；输入 → 调 `Client.Send`
- **协议**：`pkg/protocol`，wire v1 length-prefix framing

详细 ADR 见 [`AGENTS.md`](./AGENTS.md) §12。

## 快速开始

### 0. 准备（clone 后必做）

```bash
lefthook install     # 装 git 钩子
bun install          # 装 Node 侧依赖（commitlint + semantic-release）
```

### 1. 构建

```bash
make build           # 版本号从 git tag 注入
                     # bin/hub + bin/tui
```

### 2. 跑起来（两窗口演示 MVP）

```bash
# 窗口 A —— 启 Hub
go run ./cmd/hub -addr :9000

# 窗口 B —— 启 TUI 连本机 Hub
go run ./cmd/tui -hub ws://127.0.0.1:9000/ws -user alice

# 窗口 C —— 再启一个 TUI 模拟同事
go run ./cmd/tui -hub ws://127.0.0.1:9000/ws -user bob -device bob-laptop
```

关掉 B 再开，消息仍然在（`pkg/client` 内存 history + Hub 端 `FKHistoryReq` 补发）。

### 3. 跑测试

```bash
make test            # 全量单测
make lint            # golangci-lint
```

## 命令行参数

### `cmd/hub`

| flag | 默认 | 说明 |
|---|---|---|
| `-addr` | `:9000` | 监听地址（`:9000` 或 `127.0.0.1:9000`） |
| `-path` | `/ws` | WebSocket upgrade 路径 |
| `-max-history` | `500` | 单次 `FKHistoryReq` 补发的最大条数 |

### `cmd/tui`

| flag | 默认 | 说明 |
|---|---|---|
| `-hub` | — | Hub 的 ws 地址（如 `ws://192.168.1.10:9000/ws`），必填 |
| `-user` | — | 显示名（昵称即用） |
| `-device` | hostname | 设备标识（ADR-008：同用户多设备各自独立 ReadCursor） |
| `-conv` | `lobby` | 会话 ID，默认频道 `lobby` |
| `-max-hist` | `5000` | 客户端内存保留的最大消息条数 |
| `-no-connect` | `false` | 跳过连 Hub（仅用于 UI 调试，禁用交互链路） |

### TUI 内置命令

| 命令 | 作用 |
|---|---|
| `/help` | 切到 help 视图，列命令与键位 |
| `/clear` | 清屏（清当前 UI 消息与未读计数） |
| `/quit` | 退出 TUI |

键位：`Enter` 发送、`Shift+Enter` 换行、`PgUp/PgDn` 翻页、`End` 回到底部（未读自动归零）。

## 里程碑

| 里程碑 | 内容 | 状态 | 标签 / 关键交付 |
|---|---|---|---|
| M0 | 工程地基（lefthook/golangci-lint/semantic-release） | ✅ | — |
| M1 | 接口契约（`pkg/core` + `pkg/protocol` v1 + fake transport） | ✅ | `v0.1.0` |
| M2 | Hub 服务端（Router/Registry/History + WS Transport + `cmd/hub`） | ✅ | `v0.2.0` + Docker |
| **M3** | **TUI 客户端（bubbletea/v2 + 自适应 layout + / 命令 + 未读计数）** | **✅** | **`v0.3.0` 待发** |
| M4 | Web 端 | ⬜ | — |
| M5 | 多 transport（gRPC / QUIC） | ⬜ | — |
| M6 | 鉴权（Token） | ⬜ | — |
| M7 | 群聊 / 频道 | ⬜ | — |
| M8 | TLS / mTLS | ⬜ | — |

## CI

`v0.3.0` 后的依赖版本（由 Dependabot 维护）：

| 领域 | 版本 |
|---|---|
| `actions/setup-go` | v7 |
| `actions/upload-artifact` | v7 |
| `docker/login-action` | v4 |
| `docker/build-push-action` | v7 |
| `docker/metadata-action` | v6 |
| `conventional-changelog-conventionalcommits` | 10 |

CI 触发：push to `main` 跑全量测试 + lint；push tag `v*` 触发 `release.yml` 走 semantic-release + Docker 镜像发布。

## 工具链分工

| 领域 | 工具 |
|---|---|
| Go 构建 / 测试 | `make`（见 `Makefile`） |
| Go 格式化 | `gofumpt` + `gci` + `templ fmt` |
| Go 静态检查 | `golangci-lint` |
| TUI 渲染 | `bubbletea/v2` + `lipgloss/v2` + `bubbles/v2` |
| 提交信息校验 | `commitlint`（bun 跑） |
| 版本发布 | `semantic-release`（bun 跑） |
| Git 钩子 | `lefthook` |

**bun 只负责 Node 侧的两件事**：commitlint 与 semantic-release。Go 的一切归 Makefile 与 Go 工具链。

## 提交规范

Conventional Commits + scope 白名单，**原子化提交**：

```
feat(core): 定义 Transport 接口与内存 FakeTransport
fix(tui): 修复消息列表滚动到底部后跳回顶部
chore(repo): 接入 lefthook 与 golangci-lint
deps: 依赖升级（Dependabot 自动，scope=`deps`）
ci:   CI 配置变更（Dependabot 自动，scope=`ci`）
```

scope 只能是：`core` `proto` `hub` `tui` `web` `desktop` `mobile` `store` `deps` `ci` `docs` `repo` `release`

详细的提交规则见 [`AGENTS.md`](./AGENTS.md) §5。

## 版本发布

版本号、CHANGELOG、git tag 全部由 semantic-release 自动管理，**不要手动改版本号或打 tag**。

```bash
bun run release:dry   # 预演，确认算出的版本号与 notes
bun run release       # 正式发版
```

本地发版需带凭据（让 semantic-release 能 push tag + 触发 GH Release）：

```bash
CI=true GITHUB_TOKEN=$(gh auth token) bun run release
```

版本号不写入源码，构建时通过 ldflags 注入，`bin/hub --version` / `bin/tui --version` 读的是 git tag。

## 环境要求

- Go 1.27+
- bun（`~/.bun/bin`，需加入 PATH）
- `gofumpt` `gci` `templ` `golangci-lint` `lefthook`（`go install` 装）
- WSL2 需开启 `networkingMode=Mirrored`，否则局域网其它设备访问不到服务

## 许可证

[MIT](./LICENSE)
