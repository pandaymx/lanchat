# AGENTS.md — LANChat 多 AI Agent 协作规约

> 本文件是**所有 AI Agent 进入本仓库后的第一份必读文件**，也是各 Agent 的责任边界与协作纪律。
> 本地另有两份不入库的方案文档（见 `.git/info/exclude`）：`lanchat-p2p-开发方案.md`（v5 完整方案）与 `lanchat-multi-agent-路线图.md`（团队拓扑），内容以其为背景，本文件为可执行规约。

---

## 1. 项目一句话

**Go 核心 + 平台原生 UI** 的局域网即时通信系统：中心节点只做信令与文本转发，文件走客户端 P2P TCP 直连，直连失败回退 AES-GCM 加密中继。

- 核心（Go）：信令 / 鉴权 / 在线表 / P2P 传输 / 群组分发 / mDNS / 中继
- 四端原生 UI（拒绝 Web/HTML 壳）：
  - Windows = WinUI 3（C# / .NET 8），daemon 命名管道
  - Linux = GTK4 + libadwaita（Rust / gtk-rs，Relm4 可选），daemon Unix socket
  - Apple = SwiftUI（macOS daemon Unix socket；iOS gomobile XCFramework）
  - Android = Jetpack Compose（Kotlin，gomobile AAR）

---

## 2. Agent 团队拓扑

| 代号 | 角色 | scope（责任田，禁止越界） | 语言/工程 | 依赖 |
|---|---|---|---|---|
| **A0** | 契约管家 Contract Steward | `protocol` + `appapi` + `api/ipc.schema.json` | Go（纯协议，零业务依赖） | 无（一切起点，**串行**） |
| **A1** | 后端核心 Backend Core | `server` + `discover` + `relay` | Go | A0 |
| **A2** | 传输引擎 Transfer Engine | `transfer` + `group` | Go | A0 |
| **A3** | Windows 端 | `ui-win` + `ipc`（Go daemon 命名管道侧） | C#/.NET 8 + Go daemon | A0 schema |
| **A4** | Linux 端 | `ui-linux` + `ipc`（Go daemon Unix socket 侧） | Rust + gtk-rs/libadwaita | A0 schema |
| **A5** | Apple 端 | `ui-apple` + `bindings`（macOS/iOS 部分） | SwiftUI + Go(gomobile) | A0 schema |
| **A6** | Android 端 | `ui-android` + `bindings`（Android 部分） | Compose + Go(gomobile) | A0 schema |
| **A7** | 平台/发版 | `ci` + `config` + `docs` + 契约测试工具 | YAML/Go/Rust | 全部 |

**规则**：
- 一个 Agent **只允许改自己 scope 内的文件**；CI 按 scope 校验改动范围，越界拦截。
- A0 冻结契约前，其余 Agent 一律阻塞，**不得基于臆测提前写代码**。
- 契约冻结后 A1–A7 可完全并行。

---

## 3. 契约先行（最高纪律）

1. **唯一真源**：`internal/appapi` 定义方法集合 → 导出 `api/ipc.schema.json` → 四端据此实现客户端。
2. **禁止四端臆造 API**；schema 未定义的方法/字段不得出现在任何端。
3. **新增字段必须可缺省解析**；minor 版本内不允许改变已有字段语义。
4. **契约变更流程**（任何 Agent 不得单人偷改 schema）：
   1. 提「契约变更请求」，不直接改；
   2. 编排者评审并决定版本号（minor/major）；
   3. A0 + 四端（A3/A4/A5/A6）**五方同时改**并各自更新契约测试；
   4. CI 校验五端 schema 一致后才允许合并。
5. `PROTOCOL_VERSION`（`internal/protocol/version.go`）由**人工 bump**，与产品 SemVer 解耦。

---

## 4. 分支策略

- `main` 受保护，永远可发布，仅接受 **squash merge**，线性历史、禁止 merge commit。
- 每个 Agent 从基线开 short-lived 分支：`feat/a1-server`、`feat/a3-win`、`feat/a4-linux` …
- 分支命名：`<type>/<milestone>-<简述>`，如 `feat/m4-winui`、`fix/relay-deadlock`。
- 每个 PR 必须标注里程碑 **M0–M9**。

### 里程碑

| 里程碑 | 内容 | 关键 DoD |
|---|---|---|
| M0 | 契约骨架：`protocol` + Framer + fuzz + `ipc.schema.json` v1 | fuzz 30s 无 panic；单测全绿 |
| M1 | 信令闭环：Hub/Registry/Router、PSK、在线表、心跳、文本/表情 | 3 客户端互见；错误口令被拒；`-race` 通过 |
| M2 | mDNS 发现 + P2P 主干传输 | 零配置 3s 连上；1 GiB 千兆 ≥110 MB/s |
| M3 | 可靠性 + 中继：续传、多网卡拨号、反向拨号、AES-GCM 中继 | `kill -9` 可续传；`relay_force` 为密文 |
| M4 | Windows 原生端（先做，验证契约） | 发现→连接→聊天→1 GiB；契约测试通过 |
| M5 | Linux 原生端（GTK4） | Wayland 正常；四端契约测试通过 |
| M6 | Android（Compose + AAR + 前台服务） | 聊天 + 200 MB；后台保活 |
| M7 | macOS（SwiftUI + MenuBarExtra） | 完整能力 |
| M8 | iOS（前台 + 中小文件，>1 GiB 提示） | 能力边界提示生效 |
| M9 | 群组广播 G1 swarm + G2 自定义频道 | 5 成员 500 MB 全完成；消息仅成员可见；N=1 正确退化 |

端优先级固定：**Windows → Linux → Android → macOS → iOS**；M5–M8 复用 M4 验证结论。

---

## 5. 提交规范（Conventional Commits 1.0.0）

```
<type>(<scope>): <subject>
```

- **type**：`feat / fix / perf / refactor / test / docs / build / ci / chore / revert`
- **scope 枚举（写死，禁止自造）**：
  `protocol, server, core, transfer, group, relay, discover, appapi, bindings, ipc, ui-win, ui-linux, ui-apple, ui-android, config, ci, docs`
- subject 祈使句、≤72 字符、结尾无句号；type/scope 必须英文，正文可中文。
- 一个提交 = 一个可独立回滚的变更；实现与测试同提交。
- 破坏性变更必须在 footer 写 `BREAKING CHANGE:` 及迁移方式。

---

## 6. 人类门禁（HIRO，硬性约定）

- **AI 只产出 git 命令清单与变更说明，绝不代执行 `commit` / `push`，绝不自动合并。**
- 每个里程碑 / Phase 结束，Agent 输出「待执行 git 命令清单」（add 路径、commit message、tag），由人类核对后亲自执行。

---

## 7. 工程约束（设计原则落地）

- **控制面 / 数据面分离**：WebSocket 只跑控制消息（≤1 MiB），文件字节永不进 WebSocket。
- **直连优先，中继兜底**：直连失败自动回退中继；UI 显示 `直连` / `⚠ 中继` 徽章。
- Go 侧主依赖全部**无 CGO**；仅 `gomobile bind` 构建移动产物时启用 CGO。
- 桌面 / 服务端构建保持 `CGO_ENABLED=0`。
- 构建编排用 **Task（Taskfile.yml）**，不用 Makefile；调用形如 `task build`、`task ui-linux:build`。
- **默认安全**：鉴权默认 `psk`（bcrypt）；中继默认加密；文件名默认不进日志。
- **失败可恢复**：chunk 1 MiB / block 4 MiB，CRC32 + 全文件 SHA-256，`.part`+`.meta` 断点续传。

### 移动端诚实边界

- 移动端定位「控制端 + 中小文件」；iOS 后台挂起、不支持后台做种与 >1 GiB 传输，UI 不隐藏入口但需明确提示。
- `bindings/mobile` 只做薄适配层：不使用 chan / 泛型 / 复杂嵌套，事件回调用 `Listener` interface，逻辑下沉 core。

---

## 8. Mock 与解耦

- 后端未就绪时，UI 端基于 `api/ipc.schema.json` 自建 mock server（假在线列表、假传输进度），UI 独立演进。
- 后端就绪后仅替换连接地址，UI 代码零改动。

---

## 9. 测试与 CI 门禁

- Go：`gofumpt / go vet / golangci-lint`、`go test -race -cover`（core/transfer/protocol ≥70%）、Framer fuzz、集成测试。
- Rust：`cargo fmt / clippy / test`，Cargo.lock 锁版本，CI 固定 gtk4-rs/libadwaita-rs。
- **契约测试**：schema × 四端逐个校验，schema 变更未同步四端即拦截合并。
- CI = **GitHub Actions**；发版用 release-please（自动推导 CHANGELOG/版本号），R-2 冒烟通过后由**人工 merge release PR** 触发打 tag。
- 当前**无代码签名证书**：无 secrets 时签名步骤跳过，制品打 `unsigned` 标记并附内网安装指引。
