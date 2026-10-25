# CLAUDE.md — Claude Code 项目指引

> 本文件给 Claude Code 提供项目上下文与操作指引。协作规约（Agent 拓扑、契约纪律、分支/提交/人类门禁）统一见 [AGENTS.md](AGENTS.md)，**不重复维护**；两者冲突时以 AGENTS.md 为准。

## 项目简介

LANChat：Go 核心 + 平台原生 UI 的局域网 IM。WebSocket 只做信令与文本转发（控制面 ≤1 MiB），文件走 P2P TCP 直连，直连失败回退 AES-256-GCM / ChaCha20-Poly1305 加密中继（服务器不可解密）。

- 语言：Go（核心/服务端/daemon）、C#（WinUI 3）、Rust（GTK4）、Swift（SwiftUI）、Kotlin（Compose）
- 当前进度：**M0–M17 已完成**（涵盖零配置 LAN 自动选主与故障转移、持久化 X25519 身份与零配置 1:1 ECDH E2EE、服务端离线暂存与频道历史重放、语音消息与五端气泡内联图片缩略图预览）。

## 仓库布局（实际结构）

```
cmd/lanchat/                 # serve | daemon | cli | browse | version
internal/
  protocol/                  # Envelope + 消息类型 + P2P 帧 + version.go
  server/                    # Hub / Registry / Router / Spool(离线与历史缓冲) / Relay / mDNS 广告
  core/                      # 跨平台核心（无 UI 依赖，含持久化 X25519 身份、自动选主容灾、单播/群组传输与本地历史）
  transfer/                  # P2P 加密传输与断点续传
  group/                     # G1 全员广播 + G2 频道（私有频道/邀请）+ Swarm 块引擎
  relay/                     # 流式零落盘 TCP 中继配对
  discover/                  # mDNS 局域网广播与发现
  ipc/                       # JSON-RPC 2.0 桌面 IPC 服务端（Unix Socket / Named Pipe）
  appapi/                    # ★ UI 契约层（方法与通知集合，唯一真源）
  config/
api/
  ipc.schema.json            # ★ 契约机器可读定义（schema_check_test 双向校验）
  contract/*.methods.json    # ★ 五端 UI 已接入的方法清单（contract-check 门禁）
bindings/mobile/             # gomobile 薄适配层（XCFramework / AAR）
ui-win/                      # Windows WinUI 3 (.NET 8)
ui-linux/                    # Linux GTK4 + libadwaita (Rust)
ui-macos/                    # macOS SwiftUI (XcodeGen)
ui-ios/                      # iOS SwiftUI (XcodeGen)
ui-android/                  # Android Jetpack Compose (Kotlin)
scripts/                     # contract-check / check-scope / check-commit-scope / perf-baseline
test/integration/
.github/workflows/           # GitHub Actions (ci / contract / release)
configs/                     # lanchat.example.yaml / perf-baseline.json
```

## 常用验证命令

Go 核心与跨端契约门禁：

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -race ./...
go run ./scripts/contract-check/
go test ./scripts/contract-check/...
gofumpt -l . && go vet ./...
```

Linux UI（独立 Rust 工程，与 Go 核心通过 Unix socket + JSON-RPC 交互）：

```bash
cargo fmt --manifest-path ui-linux/Cargo.toml -- --check
cargo clippy --manifest-path ui-linux/Cargo.toml -- -D warnings
cargo test --manifest-path ui-linux/Cargo.toml
```

## 关键契约（实现时对齐）

- 控制面：`Envelope{ v, type, id, from, to, group, seq, replyTo, ts, payload }`，`PROTOCOL_VERSION` 与产品版本解耦。
- `group` 语义：`""` = 单播，`"*"` = 全体（G1），其他 = G2 频道 ID。
- 数据面帧：魔数 `LCTP` + ver + type + flags + length(u32) + payload；
  单播 `HELLO/ACCEPT/DATA/ACK/FIN/RESUME`，群组 `BITFIELD/REQUEST/BLOCK/HAVE`，控制 `ERROR/CANCEL/PING`。
- `appapi` 方法集合：`GetState / Connect / BrowseServers / SendText / SendSticker / OfferFile / OfferFileToGroup / ChannelCreate / ChannelJoin / ChannelInvite / ChannelLeave / ChannelList / RespondFile / CancelFile / PauseFile / ResumeFile / SetNickname / PickDownloadDir`。
- 事件：`conn.changed / peer.joined / peer.left / msg.received / transfer.progress / transfer.done / transfer.failed / group.matrix / channel.updated`。

## 编码纪律

- Go 主依赖零 CGO；`bindings/mobile` 不使用 chan/泛型/复杂嵌套，事件走 `Listener` interface。
- 文件名默认不进日志；PSK 永不打明文；路径 sanitize 并强制落在 `downloadDir`。
- chunk 1 MiB / block 4 MiB：CRC32 + 全文件 SHA-256，续传 offset 对齐 chunk/block 边界。
- 移动端诚实边界：iOS 仅前台 + 中小文件，>1 GiB 必须给出明确提示。
- **HIRO 人工终审（AGENTS.md §6）**：默认输出符合 `<type>(<scope>): <subject>` 规范的命令清单交由人类执行；当用户明确授权时，允许 AI 代执行 `git commit / push / pr / merge` 并检查 CI 状态。

