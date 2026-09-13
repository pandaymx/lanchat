# LAN Chat

局域网即时通讯，Go 优先，面向程序员用户，覆盖 Hub / TUI / Web / 桌面四端。

> 当前状态：**v2.0.0 已发布**。四端齐备：Hub / TUI / Web / 桌面
> （Windows、macOS、Linux 安装包，多架构）+ 移动端（Flutter，Android）。
>
> 功能：已读回执、Markdown/代码高亮、文件传输（图片/文件/视频）、多会话群聊、
> 消息搜索/转发/引用/表情/@提及、系统通知、会话内搜索、图片保存相册。
> 数据与下载目录自动落到平台可写位置（Windows %LOCALAPPDATA%、
> Linux ~/.local/share、macOS ~/Library/Application Support），
> 安装到 Program Files 等只读目录也能正常运行。
>
> 完整里程碑定义与子任务拆解见 [`AGENTS.md`](./AGENTS.md) §1.1。

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

# 窗口 B —— 启 TUI 连本机 Hub（自动探测 $LANG，zh_CN.UTF-8 会渲染中文 UI）
go run ./cmd/tui -hub ws://127.0.0.1:9000/ws -user alice

# 窗口 C —— 再启一个 TUI 模拟同事（强制英文 UI）
go run ./cmd/tui -hub ws://127.0.0.1:9000/ws -user bob -device bob-laptop -lang en

# 窗口 D —— 强制中文 UI（不管 env）
go run ./cmd/tui -hub ws://127.0.0.1:9000/ws -user carol -lang zh-cn
```

关掉 B 再开，消息仍然在（`pkg/client` 内存 history + Hub 端 `FKHistoryReq` 补发）。

**M3.10 真机验收要点**（zh-cn ↔ en 双窗口混跑）：

| 视觉元素 | zh-cn 文案 | en 文案 |
|---|---|---|
| 状态栏连接状态 | 在线 / 离线 | online / offline |
| 状态栏字段名 | 用户 / 设备 / 中心 / 未读 / 错误 | user / device / hub / unread / err |
| 键位提示行 | `[Enter] 发送 · [Shift+Enter] 换行 · ...` | `[Enter] send · [Shift+Enter] newline · ...` |
| 输入框占位符 | 输入消息（Enter 发送，Shift+Enter 换行） | type a message (Enter to send, Shift+Enter for newline) |
| Sidebar peers | peers：（暂无） | peers: (none yet) |

`./tui -lang-list` 能即时列出当前 binary 嵌入的所有 locale。

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
| `-log-level` | `info` | `debug`/`info`/`warn`/`error` |
| `-log-format` | `text` | `text`/`json`（生产用 `json` 接聚合） |
| `-log-file` | 空（stderr） | 日志文件路径；`M1` 不做滚动，部署侧 `logrotate` |

### `cmd/tui`

| flag | 默认 | 说明 |
|---|---|---|
| `-hub` | — | Hub 的 ws 地址（如 `ws://192.168.1.10:9000/ws`），必填 |
| `-user` | — | 显示名（昵称即用） |
| `-device` | hostname | 设备标识（ADR-008：同用户多设备各自独立 ReadCursor） |
| `-conv` | `lobby` | 会话 ID，默认频道 `lobby` |
| `-max-hist` | `5000` | 客户端内存保留的最大消息条数 |
| `-no-connect` | `false` | 跳过连 Hub（仅用于 UI 调试，禁用交互链路） |
| `-lang` | 自动探测 | 界面语言（`en` / `zh-cn` ...）；覆盖 `$LC_ALL` / `$LANG` / `$LANGUAGE` |
| `-lang-list` | — | 列出已加载的 locale 并退出 |
| `-log-level` | `info` | 同 hub |
| `-log-format` | `text` | 同 hub |
| `-log-file` | `$TMPDIR/lanchat-tui-$$.log` | bubbletea `AltScreen` 占用 stderr，必须落盘 |

### 国际化（i18n）

`pkg/tui` 的 UI chrome（状态栏、hints、help 面板、sidebar、输入框占位符、
消息行 fallback）走 `pkg/tui.Translator` 接口注入。`cmd/tui` 启动期从
`internal/i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en")` 加载 bundles，
按 `-lang` → `$LC_ALL` → `$LANG` → `$LANGUAGE` → `"en"` 优先级解析 locale，
未知 locale 自动落 fallback。

新增翻译：在 `internal/i18n/bundles/<locale>.json` 加键值，键名约定
`domain.area.item`（如 `tui.status.online`），无需改 Go 代码。

### TUI 内置命令

| 命令 | 作用 |
|---|---|
| `/help` | 切到 help 视图，列命令与键位 |
| `/clear` | 清屏（清当前 UI 消息与未读计数） |
| `/quit` | 退出 TUI |
| `/rooms` | 列出全部会话（`*` 标当前），含群成员数 |
| `/join <会话ID\|lobby>` | 切换会话（`lobby` 回大厅） |
| `/group <群名> [用户...]` | 建群（成功后自动跳转新群） |
| `/invite <会话ID> [用户...]` | 邀请用户进群 |
| `/leave` | 退当前群并回大厅 |

键位：`Enter` 发送、`Shift+Enter` 换行、`PgUp/PgDn` 翻页、`End` 回到底部（未读自动归零）。

## 里程碑

| 里程碑 | 内容 | 状态 | 标签 / 关键交付 |
|---|---|---|---|
| M0 | 工程地基（lefthook/golangci-lint/semantic-release） | ✅ | — |
| M1 | 接口契约（`pkg/core` + `pkg/protocol` v1 + fake transport） | ✅ | `v0.1.0` |
| M2 | Hub 服务端（Router/Registry/History + WS Transport + `cmd/hub`） | ✅ | `v0.2.0` + Docker |
| **M3** | **TUI 客户端（bubbletea/v2 + 自适应 layout + / 命令 + 未读计数 + slog 日志 + i18n）** | ✅ | `v0.3.0` |
| M4 | Web 端（templ + HTMX + SSE，goldmark Markdown + chroma 高亮） | ✅ | `v0.5.0` |
| M5 | 多 transport（gRPC / QUIC） | ⬜ | 未来 |
| M6 | 鉴权（Token） | ⬜ | 未来 |
| M7 | 体验打磨（分页 / 在线成员 / 正在输入 / 已读回执） | ✅ | `v0.7.x` |
| M8 | 已读回执 + Markdown/代码高亮 | ✅ | `v0.8.x` |
| M9 | 文件传输（TUI /file + Web 上传下载 / 粘贴 / 拖拽） | ✅ | `v0.9.x` |
| M10 | 桌面端（Wails v3 窗口壳 + 托盘通知 + 安装包） | ✅ | `v0.9.3` |
| M11 | 安装包矩阵（NSIS / dmg / deb / rpm，含 Windows ARM + redhat） | ✅ | `v0.13.x` |
| **M12** | **群聊 / 多会话（协议 5 帧 + Web 会话侧栏 + TUI 会话命令）** | **✅** | **`v1.0.0`** |

## v3.0 规划（移动端体验闭环）

> 于 v2.2.0 迭代期由用户点名排期；随迭代节奏推进，不做日期承诺。

| 功能 | 说明 | 端 |
|---|---|---|
| 消息长按引用 / 转发 | 长按消息 → 引用回复 / 多选转发（多选雏形已在） | 移动端 Flutter |
| 大厅用户信息卡 | 点击在线成员 / 头像弹出用户卡片（设备 / 群 / 在线态） | 移动端 + Web |
| 通知震动开关 | 设置页新增新消息通知 + 震动开关 | 移动端 |
| 表情搜索 | 表情面板支持搜索（当前仅最近表情） | 移动端 |

桌面端 QQ 化数据层待续条目（v2.2.x）：

- 会话列表未读角标 / 最后消息预览 / 时间列：需 client 侧 unread API +
  协议 ConversationSnapshot 扩展（当前 ConvView 无未读/时间/预览字段）。

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

## 移动端构建（Android / iOS）

移动端在 `apps/mobile/`（Flutter）。Android 由 CI 自动出 APK；iOS 因签名
必须在 macOS + Xcode 上做，CI 用 `--no-codesign` 产出**未签名 ipa**
（`lanchat-mobile-<版本>-ios-unsigned.ipa`）。

```bash
cd apps/mobile
flutter pub get

# Android
flutter build apk --release

# iOS（需 macOS + Xcode；--no-codesign 跳过签名）
flutter build ios --release --no-codesign
flutter build ipa --release --no-codesign   # 产出未签名 ipa
```

### iOS 自行签名安装

未签名 ipa 不能直接安装，任选一种方式：

1. **Xcode（推荐）**：`open ios/Runner.xcworkspace` → Signing & Capabilities
   选自己的 Apple ID Team → 连接 iPhone → Run。免费 Apple ID 也可（7 天
   有效，重新运行即续期）。
2. **命令行**：`flutter build ios --release`（有证书时自动签名）后
   `flutter build ipa --release`。
3. **第三方侧载**（如 Sideloadly）：导入 `lanchat-mobile-<版本>-ios-unsigned.ipa`，
   填 Apple ID 签名后安装。

> iOS 首次连接局域网 hub 会弹「本地网络」权限，允许即可。
> 语音消息需要麦克风权限、保存图片需要相册权限（首次使用时系统询问）。

## 环境要求

- Go 1.27+
- bun（`~/.bun/bin`，需加入 PATH）
- `gofumpt` `gci` `templ` `golangci-lint` `lefthook`（`go install` 装）
- WSL2 需开启 `networkingMode=Mirrored`，否则局域网其它设备访问不到服务

## 许可证

[MIT](./LICENSE)
