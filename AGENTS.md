# AGENTS.md

> 本文件是给 AI 编码助手（Claude / CodeBuddy / Cursor 等）的工程约定。
> **动手改代码前先读完。** 与口语指令冲突时，以本文件为准——除非用户明确说「这次按我说的改」。

---

## 1. 项目是什么

**LAN Chat** —— 局域网即时通讯，Go 为主，面向程序员用户，覆盖 TUI / Web / 桌面 / 移动四端。

**MVP 判据（一句话）**：一个程序员在局域网里，用两个终端窗口，能可靠地把一段代码发给同事；关掉重开消息还在；断网重连能补回漏掉的消息。

**当前阶段**：M10 桌面端已发布（apps/desktop Wails v3 窗口壳 + webapp 本地装配 + CI 原生 runner 桌面 job）；**v0.9.3 已发布**，Release 页 36 资产（6 平台 × 3 端纯 Go 矩阵）+ 桌面端 3 平台「安装包」资产（windows NSIS .exe / darwin .dmg / linux .deb，各带 sha256；按用户要求桌面端不再出 zip）。架构决策摘要内嵌于本文档 §12。桌面窗口真机运行验证待做（CI 无图形环境，见 M10 验收标准）。

### 1.1 M3 子任务拆解与进度

> 拆解依据：`pkg/tui` 代码注释里已写死的 `M3.x+` 约定 + 实际 commit 历史。
> 后续子任务若与此处不符，以用户口头决定为准，并回来改本表。

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M3.1 | 依赖与骨架 | `bubbletea/v2` + `lipgloss/v2` + `bubbles/v2` 引入，`pkg/tui` 建包 | ✅ `c214f27` |
| M3.2 | Model 骨架 | `Config`/`Model`/`Init`/`Update`/`View`，`eventMsg`/`errMsg`/`quitMsg` 适配层 | ✅ `524d2df` |
| M3.3 | 输入区 + 历史区 | `input.go`(textarea) / `history.go`(viewport) / `layout.go`(lipgloss 拼装) / `submitMsg`，**Enter 提交 · Shift+Enter 换行** | ✅ `8f951c8` |
| M3.4 | 程序入口 | `cmd/tui/main.go`：起 bubbletea Program、`FocusInput`、优雅退出 | ✅ `0d309f7` |
| M3.5 | client 接线 | client adapter：`client.Events()` → `Model.Publish`；`submitMsg` → `Sender.Send`；双 Session 经 fake.Hub 端到端验证 | ✅ `78abdb4` |
| M3.6 | 未读与滚屏 | history 切「用户在底 → 强拉尾 / 离开底 → 仅累计 unread」二分；End / PgDn 到底清零；status 显示 `unread=N` | ✅ 本 commit |
| M3.7 | 集成回归 | `TestOfflineCatchUp` 复跑 `-count=3` 全过；pkg/client 全测 `-count=3` ok；internal/integration `-count=2` ok | ✅ `f7d1c32` |
| M3.8 | 自适应与性能 | layoutDims 极值（负值/超窄/超矮/超宽）退化测试 5 个；historyView 内部 `lines` 镜像 + `AppendMessage` 增量路径避免每条 split+join | ✅ 本 commit |
| M3.9 | 收尾打磨 | `/help /clear /quit` 三命令；status 下方加键位提示行（hintsH=1）；lastError 红字 + 5s Tick 自动过期 | ✅ 本 commit |
| M3.10 | TUI i18n | `pkg/tui.Translator` 接口（注入而非 import）+ `internal/i18n` 包（embed.FS + JSON + DetectLocale）+ `cmd/tui -lang` flag；14 处 UI 文案走 i18n，bundles `en` / `zh-cn` 双语 | ✅ `6fa8af4` → `5ce8c5c` → `96a60d6` → `017ea68` → `f94ab72` |

**M4 子任务拆解与进度**（当前里程碑，M4.1–M4.3 已合入）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M4.1 | Web 方案拍板 | SSE（EventSource 原生，反代零配置）+ thin proxy（复用 pkg/client.Client）+ MVP 单会话（ADR-011/012） | ✅ proposal 本地保留（docs/proposals/ 不入库） |
| M4.2 | Web 骨架 | `cmd/web` -addr :9001 + 三路由占位 + templ 模板 + vendored htmx/sse-ext 内嵌 + CI/Makefile 适配 | ✅ `007a173` → `918fc53` → `004ba51` |
| M4.3 | handler 接 client | `internal/webui/session.go` DialClient 装配 + SSE 推流 + POST /api/messages Send + fake hub E2E 双 web 互发 | ✅ `3e635a0` → `70f4269` → `32180e1` |
| M4.4 | 多 Tab Session | cookie 签发 + Manager.GetOrCreate/Release + Session.fanout 多 SSEWriter 订阅 | ✅ `a792f82` → `e50e221` → `97c7626` |
| M4.5 | 输入体验 | Enter 提交 / Shift+Enter 换行（输入法组词保护）、断连 banner（SSE state 帧 swap）、历史「加载更多」（proto Before + hub Query + client.FetchHistory 静默分页 + /history 端点） | ✅ `3b1bd56` → `8d8291e` → `1a09ec6` → `a5d1cc9` → `07d4dbe` → `c1c1ff8` |
| M4.6 | 自动重连 | EventSource 重连 + Last-Event-ID 断线补发（catchUp 补写 + sseChunk 带 seq + writer skipSeq 幂等去重） | ✅ `6c7ae15` |
| M4.7 | Web i18n | 复用 internal/i18n bundle：templates.Translator 窄接口 + T() nil 兜底，Config/ManagerConfig 注入，cmd/web -lang/-lang-list flag | ✅ `126f541` |

**M5 子任务拆解与进度**（持久化，M5.1–M5.2 已合入）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M5.1 | libSQL store | `pkg/store/libsql` 实现 core.Store（libsql-client-go + blank import modernc.org/sqlite 注册本地引擎，纯 Go 零 CGO）；五表幂等迁移；UPSERT/History 升序/游标 MAX 单调；MaxSeq + RecentMessages 恢复原语 | ✅ `f631656` |
| M5.2 | hub 接线与重启恢复 | `cmd/hub -db` flag（默认 `lanchat.db` 文件库，`-db memory` 纯内存）；StartSeq=MaxSeq 防序号撞车；RecentMessages 灌回内存补发缓冲（HistoryRestoreLimit=5000）；TestRouterRestoreFromStore | ✅ `de53143` → `2d44610` |
| M5.3 | 文档收尾 | AGENTS.md 技术栈/进度表更新 | ✅ 本 commit |

**M5 验收标准**：hub 关掉重开消息还在；重启后新消息序号接续不撞号；客户端离线补发在重启后仍可用；`CGO_ENABLED=0` 全平台（linux/arm64、windows/amd64）构建通过。

**M6 子任务拆解与进度**（mDNS 服务发现）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M6.1 | 发现包 | `internal/discovery`（grandcat/zeroconf 纯 Go，零 CGO）：Broadcast 注册 `_lanchat._tcp`（TXT 带 ws path/version）、Discover 浏览去重排序、DiscoverHubURL 返回首个实例 ws URL；首个实例后 800ms grace 提前返回（实测 ~1.4s）；无组播环境 ErrNoHub 降级 | ✅ `00e45de` |
| M6.2 | 三端接线 | cmd/hub `-mdns`（默认开）广播；cmd/tui `-hub` 留空自动发现；cmd/web `-hub-url` 留空自动发现；实测 web 无参启动 → 发现 `ws://192.168.1.47:19000/ws` → dial ok | ✅ `a50f3a6` |
| M6.3 | 文档收尾 | AGENTS.md M6 进度表 | ✅ 本 commit |

**M6 验收标准**：hub 启动后局域网内 tui/web 不带地址参数即可自动发现并连上；`-mdns=false` / 显式 `-hub` 仍可手动指定；CGO_ENABLED=0 交叉编译不受影响。

**M7 子任务拆解与进度**（体验打磨）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M7.1 | TUI 上翻分页 | PgUp 到顶自动 FetchHistory（Before 分页，50 条/页）；Session 实现 HistoryFetcher 窄接口；去重合并 + YOffset 锚点不跳屏；HasMore 到头停止；失败可重试 | ✅ `27b435d` |
| M7.2 | 在线成员端到端 | hub 握手后发 roster + 广播 FKPresence（下线广播带重连防抖）；Client 维护在线名单快照 Peers()；TUI 侧栏首次有数据；Web 在线成员条（首屏渲染 + presence SSE 帧 swap，自己标「(you)」） | ✅ `7ee980e` → `ac6255d` → `576200f` |
| M7.3 | 正在输入指示 | FKTyping 帧：客户端发空负载、hub 按注册表盖戳身份广播（防伪造）；Client 维护 typing 快照（6s TTL 惰性过期、按 UserID 去重、消息/下线清除）；TUI 输入 3s 节流上发 + hints 行「X 正在输入…」；Web `/typing` 端点（POST 上发 / GET 自刷新）+ typing SSE 帧，片段 6s 自刷新清除、htmx leading throttle 上发 | ✅ `fb5e281` → `93048e0` → `f2d59ac` → `7487d5f` → `af4a02a` |

**M8 子任务拆解与进度**（消息体验闭环；M8=①已读回执+②Markdown，M9=③文件传输，M10=④桌面端——顺序经用户确认）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M8.1 | 已读回执端到端 | 协议 FKRead/ReadCursor（per-device 游标，ADR-008 硬要求）；hub 盖戳广播（防伪造，同 typing 信任模型）+ 握手快照补发；Client ReadCursors/SendRead；Store read_cursors 持久化；TUI 他人已读游标快照 + 「✓已读」标记 + 贴底/新消息自动上发（Reader 接口）；Web `/read` 端点 + `event: read` SSE 帧 + app.js 打勾/上发（首屏标记 + 滚动贴底上发） | ✅ 本 commit |
| M8.2 | Markdown + 代码高亮 | Web：goldmark（GFM + WithHardWraps）服务端渲染 + chroma monokai 高亮，原始 HTML 剥离（goldmark WithUnsafe 未开，`<!-- raw HTML omitted -->`），`templ.Raw` 注入；TUI：glamour 定制 dark 样式（WordWrap 0 / 去段落填充缩进 / 纯文本不染色，探针验证），按消息 ID 缓存渲染结果；两端零协议改动，依赖纯 Go 无 CGO | ✅ 本 commit |

**M8 验收标准**：A 设备发的消息在 B 设备贴底后，A 端（TUI/Web）出现「✓已读」标记且重连补发不丢；消息体支持 Markdown 渲染与代码高亮，`<script>` 等原始 HTML 在两端均不执行/不显示；纯文本消息渲染与旧版一致。

**M9 子任务拆解与进度**（文件传输；M9=文件传输，M10=桌面端——顺序经用户确认；打包仍为压缩包非安装包，hub/tui/web 拆独立压缩包）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M9.1 | 协议与存储 | `StoredMessage.File *FileRef`（json `f,omitempty`）；FileMeta 表 + messages 附件四列幂等迁移；Store `SaveFileMeta/GetFileMeta` | ✅ 本 commit |
| M9.2 | hub blob 服务 | `pkg/hubfile`（crypto/rand 32hex FileID 防遍历、文件名 sanitize 绝不入路径、超限清理）；`POST/GET /api/files` 与 WS 同端口同 mux（`ws.Transport.WithHandler`）；cmd/hub `-files`/`-max-file-size`（默认 512MiB） | ✅ 本 commit |
| M9.3 | client 文件 API | `HTTPBaseFromWS`（ws→http，裸地址补 ws://）、`SetFileBase`、`UploadFile`（流式 multipart 不整缓冲）、`SendFileMessage`、`DownloadFile`（404→ErrNotFound）；未配置返回 `ErrFileUnconfigured` | ✅ 本 commit |
| M9.4 | TUI 文件收发 | `/file <path>` 命令；附件卡片 `[文件] name (size)` + 「已保存」标记；他人附件实时到达自动下载到 `lanchat-files/`（自己回环跳过）；i18n `tui.file.*` | ✅ 本 commit |
| M9.5 | Web 文件收发 | `/api/files` 代理端点（上传 multipart 透传 + 下载 Range/Content-Type/Content-Disposition 透传）；附件卡片 / 图片内联预览；📎 按钮 + 剪贴板粘贴 + 拖拽上传 | ✅ 本 commit |
| M9.6 | 打包拆分 | release.yml：每个二进制独立压缩包 `lanchat-<bin>-<ver>-<os>-<arch>.{tar.gz,zip}` + 独立 sha256；Windows 同样三份 zip | ✅ 本 commit |

**M9 验收标准**：TUI `/file` 或 Web 📎/粘贴/拖拽上传后，对端 TUI 自动下载到 `lanchat-files/` 并显示「已保存」，Web 端渲染附件卡片（图片内联预览、其余可下载）；hub 重启后文件仍可下载（blob 落盘 + 元信息入 store）；文件名带路径穿越字符时不越权读写；release 产物为按端拆分的压缩包（非安装包）。

**M10 子任务拆解与进度**（桌面端；M10=桌面端——顺序经用户确认，技术选型见 ADR-015）：

| # | 主题 | 交付物 | 状态 |
|---|---|---|---|
| M10.1 | 窗口化客户端 | `apps/desktop/main.go`（`//go:build desktop`）：Wails v3 窗口 Navigate 到本地 webui；`apps/desktop/webapp` 纯 Go 装配（127.0.0.1 随机端口，Start/Close 生命周期，可单测）；hub 地址 mDNS 自动发现 / `-hub-url` 指定；`main_stub.go`（`!desktop`）保默认构建全绿 | ✅ 本 commit |
| M10.2 | 托盘与通知 | 系统托盘（systray）+ 新消息系统通知；独立 CGO 依赖，随壳单列构建 | ⬜ 待做 |
| M10.3 | 打包 | release.yml `desktop` job（原生 runner × 平台）产「安装包」：windows NSIS 向导安装器（.github/installer/desktop.nsi，Program Files + 开始菜单 + 卸载注册表）、darwin .dmg（.app bundle + ad-hoc 签名 + /Applications 软链）、linux .deb（Depends 声明 gtk4/webkitgtk 运行时，apt 自动装依赖）；脚本在 .github/installer/ | ✅ 本 commit |

**M10 验收标准**：桌面窗口打开即连 hub（自动发现或手动指定），Web UI 全部功能可用（消息/已读/Markdown/文件）；关闭窗口进程退出、本地 server 随之释放端口；`go build ./...`（无 tag）与 CI 纯 Go 矩阵不含桌面端且全绿；release 产物 `lanchat-desktop-*` 压缩包可下载运行。

**M3 验收标准（对应方案 §11.5）**：两终端聊天；断网重连自动补发；历史可滚动；代码块可复制。

---

## 2. 技术栈（硬性约束，不要提议替换）

| 项 | 选择 | 约束 |
|---|---|---|
| 语言 | Go 1.27 | |
| 前端 | templ + HTMX | **不引入 React/Vue/任何 JS 框架，不引入前端构建工具** |
| 传输 | WebSocket（一期唯一实现） | 必须走 `Transport` 接口 |
| 存储 | libSQL 纯 Go 驱动 | **libsql-client-go + blank import modernc.org/sqlite 本地引擎，纯 Go 无 CGO（ADR-013）**；DSN 用 `file:<path>` |
| 任务入口 | Makefile（Go 侧）/ package.json（Node 侧） | 不用 bun 包办 Go 命令 |
| Node 运行时 | bun 1.3.14 | **只用于 commitlint 与 semantic-release** |

**禁止引入**：任何需要 CGO 的依赖、JS 前端框架、ORM（手写 SQL）、除标准库 `log/slog` 外的日志库。

**唯一 CGO 例外（ADR-015）**：`apps/desktop`（桌面壳）允许 CGO（系统 WebView：
Linux webkit2gtk / Windows WebView2 / macOS WKWebView），这是全仓唯一的
例外。约束：桌面端代码只允许出现在 `apps/desktop`，且 CGO 相关文件一律带
`//go:build desktop` tag——默认 `go build ./...` / `go test ./...` 不编译
桌面端，`CGO_ENABLED=0` 交叉编译矩阵不受影响；桌面端单独由 CI 原生 runner
job 构建（见 §6 发布流程）。`pkg/` 保持纯 Go（gomobile 可复用）。

**关键库的选型理由**：WebSocket 用 `coder/websocket`（gorilla 已归档）；存储用 libSQL 纯 Go 驱动（ADR-013）：`libsql-client-go` 走标准 `database/sql`，本地 `file:` DSN 自身不带引擎，必须 blank import `modernc.org/sqlite`（注册名 "sqlite"）——禁用 CGO 版 `mattn/go-sqlite3`（会毁掉交叉编译与 gomobile），也不引 CGO 版 go-libsql。

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
│   ├── i18n/          # 共享 i18n bundle 加载器（embed.FS JSON，被 cmd/tui 引用；pkg/tui 自身不 import）
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

发布流程（2026-09-07 起 main 分支语义化 commit 全自动发版）：

1. push 到 main 且含 `feat`/`fix` 等语义化 commit → CI（release.yml）自动：
   semantic-release 算版本号 → 更新 CHANGELOG → 提交 `chore(release): x.y.z
   [skip ci]`（不触发新 run）→ 打 tag（GITHUB_TOKEN push 不触发新 run，
   无循环）→ 创建 GitHub Release → 同一 run 内 package job 交叉编译
   6 平台（linux/darwin/windows × amd64/arm64），并按端拆独立压缩包：
   `lanchat-<hub|tui|web>-<ver>-<os>-<arch>.tar.gz|zip`（各带 .sha256），
   共 18 个压缩资产 + 18 个校验文件。桌面端（ADR-015 CGO 例外）由同一 run 的 `desktop` job 在原生 runner 构建「安装包」：`lanchat-desktop-<ver>-<os>-<arch>.{exe,dmg,deb}` + sha256（windows NSIS 安装器 / darwin dmg / linux deb，架构取 runner 原生 GOARCH；按用户要求桌面端不再出 zip）。
2. 手动兜底：`bun run release:dry` 本地预检算出的版本号与 notes
   （需 `export GH_TOKEN=<PAT，repo scope>`；gh CLI 的 OAuth token 过不了
   @semantic-release/github 的权限校验）。零配置的 dry-run 路径是 Actions
   里 Release workflow 的 workflow_dispatch（dryRun=true）。需要手动补跑
   发布时同样用 workflow_dispatch（dryRun=false），打包随之在同一次 run
   内完成。
3. 不发版时不要手动改版本号 / 打 tag——版本由 commit 类型决定。
   **打包版本号不能取 `git describe`**：HEAD 停在 push 的 commit，新 tag
   指向 semantic-release 的 chore commit（HEAD 的子提交），describe 只会
   回溯到旧 tag（2026-09-07 实测拿到 v0.5.0）。CI 用 release job 的
   Detect step（git tag 前后对比）输出实际版本，经 `LANCHAT_VERSION`
   传给 package job。

**两个已踩过的坑（不要重复踩）**：

1. **`conventional-changelog-conventionalcommits` 必须锁 `^8`**。v10 依赖 `conventional-changelog-writer@9`，而 semantic-release 25 内置的是旧版 writer，generateNotes 阶段会报 `Missing helper` 直接失败。
2. **初始版本号**。没有历史 release 时，semantic-release 会把首次发布算成 **`1.0.0`**，而不是版本路线里的 `0.1.0`。已用 `git tag v0.0.0` 标记项目起点，此后 `feat` 才会算出 `0.1.0`。
   **建好远端仓库后必须 `git push origin v0.0.0`**，否则远端找不到 previous release，又会跳回 1.0.0。

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

**已踩过的坑（不要重复踩）**：

| 坑 | 现象 | 正解 |
|---|---|---|
| Go 模块代理不通 | `proxy.golang.org` dial tcp 不可达 | 已设 `GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=sum.golang.google.cn` |
| `go install lefthook` 失败 | `undefined: json.SkipFunc`（依赖与 Go 1.27 不兼容） | **lefthook 用 bun 装**：`bun add -d @evilmartians/lefthook`（预编译二进制） |
| golangci-lint 装成 v1 | `@latest` 只给到 v1.64.8，与 v2 配置不兼容 | v2 模块路径带 `/v2`：`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |
| `go build ./...` 污染根目录 | 仅一个 main 包时，二进制被写到项目根目录并被 git 提交 | 必须写成 `go build -o bin/ ./...`（见 lefthook.yml 注释） |
| semantic-release 报 ENOREPOURL | 没有 git remote，无法确定仓库地址 | 需先添加远端仓库；`.releaserc.json` 里的 `repositoryUrl` 目前是占位值 |
| revive `package-comments` | 包缺少包注释导致 lint 失败 | 每个包（含 `main`）顶部加 `// Command xxx ...` 或 `// Package xxx ...` |

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

1. 先读 `AGENTS.md`「当前阶段」段确认当前里程碑与验收标准
2. 改代码 → `make fmt` → `make lint` → `make test`
3. 用 `git add -p` 分块暂存，按 §5.2 拆成原子提交
4. 提交信息按 §5.1 写
5. **不要**在 commit message 里加 `Co-Authored-By` 之类的自动签名，除非用户要求

## 11. 日志规范

**库选择**：标准库 `log/slog`（Go 1.21+）。不引入 zap/zerolog —— 与本仓库零 transitive 依赖策略一致。

**包**：统一通过 `pkg/logging` 门面，**不要**直接 `import "log/slog"` 然后 `slog.Info(...)`。

### 11.1 三层 API

| 调用 | 谁用 | 何时调 |
|---|---|---|
| `logging.Init(level, format, file)` | `cmd/hub/main.go` / `cmd/tui/main.go` | main 里调一次，注入全局 handler |
| `logging.New("pkg-name")` | 任意 pkg 包级 var | 拿带 `component=pkg-name` attr 的 logger |
| `xxx.Info/Warn/Error/Debug(msg, "k", v, ...)` | 业务代码 | 触发日志 |

### 11.2 关键 gotcha：不要用 `slog.Default().With(...)`

```go
// ❌ 错误：包级 init 时固化 stdlib 默认 handler
var log = slog.Default().With("component", "x")
// logging.Init 在 main() 调 SetDefault 时不会替换这里已派生的 logger
// stdlib 默认 handler 把 level 字符串 format 进 message 字段
// 出现 `msg="INFO xxx component=x ..."` 这种污染

// ✅ 正确：pkg/logging.ComponentLogger 每次走当前 slog.Default()
var log = logging.New("x")  // 返回 *logging.ComponentLogger
log.Info(...)  // 内部走 slog.Default().Log(...)
```

回归测试见 `pkg/logging/logging_test.go:TestComponentLogger_PicksUpNewDefault`。

### 11.3 flag

| cmd | flag | 默认 |
|---|---|---|
| `hub` | `-log-level` | `info` |
| `hub` | `-log-format` | `text`（生产用 `json` 接 Loki/CloudWatch） |
| `hub` | `-log-file` | 空（走 stderr） |
| `tui` | `-log-level` | `info` |
| `tui` | `-log-format` | `text` |
| `tui` | `-log-file` | `$TMPDIR/lanchat-tui-$$.log`（AltScreen 占用 stderr，必须落盘） |

### 11.4 埋点原则

- **不要每个函数都埋**：业务关键路径（connect/disconnect、send/receive、broadcast、catch-up sequence、permission deny）必须埋
- **热路径低开销**：高频循环（每帧渲染、每条消息都走）只走 Debug 级别，默认 Info 不输出
- **错误必埋**：所有 `return err` 之前必须 `log.Error(...)` 或留 fmt 包里的 error
- **不要 print 日志**：调试残留用 Debug 级别 + `git grep -nP 'fmt\.Println|Fprintf\(os\.Stderr'` 自查

### 11.5 限制（M1 阶段不引入）

- ❌ log file 滚动（lumberjack） → 部署侧 logrotate
- ❌ 结构化 trace/span 字段 → 留接口位等 OpenTelemetry
- ❌ request_id 串联 → 等真有多步调用场景再加
- ❌ 接 Loki/CloudWatch → M6 部署阶段

## 12. 不确定时

- 架构层面的取舍 → 先用文字讨论，得到共识再写代码；讨论结果记录在 `git commit` / PR 描述
- 本文件有歧义或过时 → 直接改本文件，并在 commit message 里说明
- 用户没明确要求的重构/优化 → 不做（偏好最小改动）

---

## 14. ADR 摘要

按时间倒序。完整讨论见对应 commit / PR。

### ADR-015：桌面端技术选型（2026-09-07，M10）

- **背景**：Web UI 已完整（M4–M9 全能力），M10 需要原生窗口壳。规划时预设 Wails。
- **推翻 Wails 的事实**：Wails v2 不支持窗口加载外部 URL（需 redirect hack）；其核心机制
  （embedded Assets + Go/JS binding）对本项目无用；v2 托盘/通知也不内置；构建需 wails CLI +
  frontend 目录。Wails v3 支持外部 URL 但仍 alpha。
- **选型**：`github.com/webview/webview_go`（MIT，系统 WebView 绑定，API 极简
  `New/Navigate/SetTitle/Run`，`Navigate("http://127.0.0.1:<port>")` 直接加载本地服务）。
  已排除 `modernc.org/webview`（零 CGO 但自研玩具渲染引擎，渲染不了 htmx+SSE 应用）。
- **CGO 例外**：webview_go 需要 CGO（webkit2gtk / WebView2 / WKWebView），是全仓唯一 CGO 例外。
  隔离机制：`apps/desktop` 下 CGO 文件带 `//go:build desktop`，默认构建不含桌面端；
  桌面 job 在 CI 原生 runner 单列（§6）。
- **窗口形态**：桌面进程内起 webui handler（127.0.0.1 随机端口），窗口加载该地址；
  关闭窗口 → 进程退出 → 端口释放。hub 连接复用 mDNS 自动发现 / `-hub-url`。
- **首版范围**：M10.1 窗口化客户端；M10.2 托盘/通知（systray，又一层 CGO）后续再做。
- **打包**：`lanchat-desktop-<ver>-<os>-<arch>.{tar.gz,zip}` + sha256，沿用 M9 拆包模式。

---

## 13. 国际化（i18n，落地于 M3.10）

**目的**：仅给 TUI UI chrome 提供本地化字符串；hub 端日志、协议消息体不在翻译范围。

**架构铁律**：

1. `pkg/tui` 不得 import `internal/i18n`（保持 `pkg/` 与 `internal/` 边界）。
   解决办法：`pkg/tui` 内置 `Translator interface { T(key string) string }`，
   `cmd/tui` 启动期调 `internal/i18n.MustLoadEmbedded(...)` 拿到 bundle 后
   把 `bundle.ForLocale(locale)` 注入 `tui.Config.Translator`。
2. bundle 文件用 JSON + `embed.FS` 嵌入，零 codegen。
3. locale key 一律小写存储：`bundles/zh-cn.json`，Load/T/Tf 内部 `strings.ToLower`，
   允许调用方传 `"zh-CN"` / `"ZH-CN"` / `"zh-cn"` 都命中。**但文件名要小写**。
4. fallback 链：locale → `fallbackLocale` (en) → key 字面值。
5. key 命名约定：`domain.area.item`（flat，不分 namespace）。

**使用入口**：

| 调用方 | 路径 |
|---|---|
| `pkg/tui` UI 文案 | `m.t("tui.status.online")` → 走 Config.Translator |
| `cmd/tui` 启动 | `bundle := i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en")`<br>`locale := i18n.DetectLocale(os.Environ(), "en")`<br>`cfg.Translator = bundle.ForLocale(locale)` |
| `cmd/tui` flag | `-lang <locale>`（flag 优先 → env → fallback）· `-lang-list` |

**新增翻译**：在 `internal/i18n/bundles/<locale>.json` 加键值，无需改 Go 代码。

**测试要求**：

- `internal/i18n`：17 个测试覆盖 Load / T / Tf / ForLocale / LocateSet / DetectLocale（BCP 47 简化）
- `pkg/tui`：`fakeTranslator` + `called/calledCount/SetLocale` 测试 14 key 都被命中文案
- `cmd/tui`：`TestBundlesEndToEnd_ZHCN` 走完整链路（embedded bundle + DetectLocale → 中文返回）
- `internal/webui`：`testTranslator`（zh-cn bundle）注入 Config/Manager，现有中文断言不动；
  `fakeTranslator` 收集渲染期 key，`TestWebI18N_AllChromeKeysUsed` 断言 6 个 `web.*` key 全命中
- agents-check 钩子（`.agents/tools/check.sh`）：en/zh-cn key 集合一致 + 引用 key 必须存在，
  引用扫描覆盖 `pkg/tui`（`.t("...")`）与 `internal/webui`（`.go` / `.templ` 里 `"web.*"`）
