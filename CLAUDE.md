# CLAUDE.md — Claude Code 项目指引

> 本文件给 Claude Code 提供项目上下文与操作指引。协作规约（Agent 拓扑、契约纪律、分支/提交/人类门禁）统一见 [AGENTS.md](AGENTS.md)，**不重复维护**；两者冲突时以 AGENTS.md 为准。

## 项目简介

LANChat：Go 核心 + 平台原生 UI 的局域网 IM。WebSocket 只做信令与文本转发（控制面 ≤1 MiB），文件走 P2P TCP 直连，直连失败回退 AES-256-GCM 加密中继（服务器不可解密）。

- 语言：Go（核心/服务端/daemon）、C#（WinUI 3）、Rust（GTK4）、Swift（SwiftUI）、Kotlin（Compose）
- 当前进度：**M0 之前**——仓库刚初始化，尚未生成代码；按路线图从 **M0（A0 契约）** 起步。

## 仓库布局（目标结构）

```
cmd/lanchat/                 # serve | daemon | cli | browse | version
internal/
  protocol/                  # Envelope + 消息类型 + P2P 帧 + version.go
  server/                    # Hub / Registry / Router / Relay / mDNS 广告
  core/                      # 跨平台核心（无 UI 依赖）
  transfer/                  # P2P 传输（含 swarm 分发器）
  group/                     # G1 广播 + G2 频道
  discover/                  # mDNS
  appapi/                    # ★ UI 契约层（方法集合，唯一真源）
  config/
api/
  ipc.schema.json            # ★ 契约机器可读定义（生成 + 校验）
bindings/mobile/             # gomobile 薄适配层（XCFramework / AAR）
ui/{windows,linux,macos,ios,android}/
test/integration/
.github/workflows/           # GitHub Actions
configs/lanchat.example.yaml
```

## 开工顺序（必须遵守）

1. **先 M0**：`internal/protocol`（Envelope + 全部消息类型 + P2P 帧，含群组帧占位）、Framer、fuzz/单测、`api/ipc.schema.json` v1、`internal/appapi` 接口桩。
2. M0 经人类评审冻结、写入 `PROTOCOL_VERSION` 后，才并行推进 A1–A8（macOS=A5、iOS=A6、Android=A7、平台/发版=A8）。
3. 每个里程碑结束输出「待执行 git 命令清单」，**等待人类亲自执行 commit/push**，不要自行提交。

## 常用命令

构建编排统一使用 Task（不使用 Makefile）：

```bash
task build               # 服务端 + daemon + CLI（CGO_ENABLED=0）
task dist                # linux/windows/darwin 产物（并行）
task bind-ios            # gomobile bind → LanchatCore.xcframework
task bind-android        # gomobile bind → lanchatcore.aar
task ui-linux:build      # 各端各自子任务（ui-windows/ui-macos/ui-ios/ui-android 同理）
```

M0 阶段直接使用 Go 工具链：

```bash
go build ./...
go test -race ./...
go test -fuzz=Framer -fuzztime=30s ./internal/protocol
gofumpt -l . && go vet ./...
```

Linux UI（独立 Rust 工程，与 Go 核心仅靠 Unix socket + schema 交互）：

```bash
cargo fmt --check && cargo clippy -- -D warnings && cargo test
```

## 关键契约（实现时对齐）

- 控制面：`Envelope{ v, type, id, from, to, group, seq, replyTo, ts, payload }`，`PROTOCOL_VERSION` 与产品版本解耦。
- `group` 语义：`""` = 单播，`"*"` = 全体（G1），其他 = G2 频道 ID。
- 数据面帧：魔数 `LCTP` + ver + type + flags + length(u32) + payload；
  单播 `HELLO/ACCEPT/DATA/ACK/FIN`，群组 `BITFIELD/REQUEST/BLOCK/HAVE`，控制 `ERROR/CANCEL/PING`。
- appapi 方法集合：`GetState / Connect / BrowseServers / SendText / SendSticker / OfferFile / OfferFileToGroup / RespondFile / Pause/Resume/CancelFile / SetNickname / ChannelCreate / ChannelJoin / ChannelList`。
- 事件：`conn.changed / peer.joined / peer.left / msg.received / transfer.progress / transfer.done / transfer.failed / group.matrix / channel.updated`。

## 编码纪律

- Go 主依赖无 CGO；`bindings/mobile` 不使用 chan/泛型/复杂嵌套，事件走 `Listener` interface。
- 文件名默认不进日志；PSK 永不打明文；路径 sanitize 并强制落在 downloadDir。
- chunk 1 MiB / block 4 MiB：CRC32 + 全文件 SHA-256，续传 offset 对齐 block 边界。
- 移动端诚实边界：iOS 仅前台 + 中小文件，>1 GiB 必须给出明确提示，不假装能做到。
- 提交信息严格遵循 Conventional Commits 与 AGENTS.md 中写死的 scope 枚举。
