## [0.3.0](https://github.com/pandaymx/lanchat/compare/v0.2.0...v0.3.0) (2026-09-05)

### Features

* **tui:** 接 Session 适配层，让 submitMsg 经 Sender 真正发到 hub ([78abdb4](https://github.com/pandaymx/lanchat/commit/78abdb4089294362bcaed57387cc85533b7bf594))
* **tui:** 引入 bubbletea/v2 + lipgloss/v2 + bubbles/v2 依赖并建立 pkg/tui 骨架 ([c214f27](https://github.com/pandaymx/lanchat/commit/c214f270027db8355e3ea9ae1ff491a98d9cfddd))
* **tui:** 增加 cmd/tui 程序入口，启用 alt screen 与尺寸就绪后聚焦 ([0d309f7](https://github.com/pandaymx/lanchat/commit/0d309f72f37a1d21595bbcad145bebad804def7e))
* **tui:** 增加 textarea/viewport 区域与 Enter/Shift+Enter 键位路由 ([8f951c8](https://github.com/pandaymx/lanchat/commit/8f951c86f14fe8df88772937aaa29a45b05f41e1))
* **tui:** M3.6 未读计数 + 滚屏路由（用户离底时不强拉） ([c9be141](https://github.com/pandaymx/lanchat/commit/c9be1419f34494312bc03d4735345bd00ef0659d))
* **tui:** M3.8+M3.9 自适应 + 收尾打包 ([8fbb909](https://github.com/pandaymx/lanchat/commit/8fbb909ad3cba42f31a26b3d1cd684444b8900bd))
* **tui:** Model 骨架 + eventMsg 适配层 ([524d2df](https://github.com/pandaymx/lanchat/commit/524d2df0375df2438e9532285a544ac7c0285631))

### Bug Fixes

* **ci:** 启用 go module cache + 放宽 ws_hub 集成测试等待超时 ([d880756](https://github.com/pandaymx/lanchat/commit/d880756d310ec4dcae5dd3ad36e5bdc738e53880))
* **core:** catch-up 窗口护栏防止 FKDeliver 抢跑 FKHistoryResp ([6b3e216](https://github.com/pandaymx/lanchat/commit/6b3e216d303269f924186184c98f32903cf98605))
* **repo:** 用 eventually() 重试 helper 修复 race+cover 偶发 5s 超时 ([21a8901](https://github.com/pandaymx/lanchat/commit/21a89014c844b4be7e1ebec5463014b1fa30cfa5))

## [0.2.0](https://github.com/pandaymx/lanchat/compare/v0.1.0...v0.2.0) (2026-09-05)

### Features

* **core:** 定义 hubstate.Peer 抽象与 ServerSeq 分配器 ([5772bd8](https://github.com/pandaymx/lanchat/commit/5772bd8a8a44750275dd78e5c24416542073b746))
* **core:** 实现 hubstate.History 有界补发缓冲 ([1c64709](https://github.com/pandaymx/lanchat/commit/1c6470919b65bdedc08e3d3a382a53b6427a9540))
* **core:** 实现 hubstate.Registry 支持多设备一对多投递 ([5b21f7b](https://github.com/pandaymx/lanchat/commit/5b21f7b94f3e2296affcd8d4ab07c272c4908e21))
* **core:** 实现 hubstate.Router 帧路由与连接生命周期 ([0c19b14](https://github.com/pandaymx/lanchat/commit/0c19b14bb0c5878bb7e1bf138b87d670c084c769))
* **deps:** 引入 coder/websocket 与 oklog/ulid 依赖 ([5e94078](https://github.com/pandaymx/lanchat/commit/5e94078bbac866985ad86085e613d1c00cb74f5a))
* **hub:** cmd/hub 接入 Router + WS Transport + 内存 Store 并支持信号关停 ([8224b79](https://github.com/pandaymx/lanchat/commit/8224b7997487246b57cc9dffce80e15933552d76))
* **proto:** 实现 WebSocket Transport 满足 core.Conn 与 hubstate.Peer ([7dc5f2d](https://github.com/pandaymx/lanchat/commit/7dc5f2d9daa691b6309543e6d7e4e38ea6b8db8b))

### Bug Fixes

* **repo:** lefthook gci 命令补 --no-lex-order 与 sections，保留本项目 import 空行 ([b1fdcfd](https://github.com/pandaymx/lanchat/commit/b1fdcfde443460db34c5d2339b8fe8180439f740))

## [0.1.0](https://github.com/pandaymx/lanchat/compare/v0.0.0...v0.1.0) (2026-09-05)

### Features

* **core:** high-level Client API with 4 acceptance tests ([3370ec5](https://github.com/pandaymx/lanchat/commit/3370ec5c44e8fdc8487ef6169e68b7acebe89a9a))
* **core:** in-process FakeTransport with Hub for tests ([723c08f](https://github.com/pandaymx/lanchat/commit/723c08fe1aac4efa2a005363421de375fe1e2b63))
* **core:** local fan-out EventBus with bounded subscriber channels ([aa580cc](https://github.com/pandaymx/lanchat/commit/aa580cc3431baabeb4e5dc565e131bfafe20d796))
* **core:** transport/store/eventbus interface contract ([7bafd4c](https://github.com/pandaymx/lanchat/commit/7bafd4c99ad5f2dbbd6bef6579bac7b538604a0f))
* **hub:** 添加 Docker 多阶段镜像、build ignore 与 compose 文件 ([6fb69be](https://github.com/pandaymx/lanchat/commit/6fb69be7a1c3af2862a34147de3538ae84f5075a))
* **proto:** wire protocol v1 with length-prefix framing ([6aeb001](https://github.com/pandaymx/lanchat/commit/6aeb0014eba5de7860830e536235640f239d7680))
* **store:** thread-safe in-memory Store with upsert-by-ID ([282dc3b](https://github.com/pandaymx/lanchat/commit/282dc3b6af1d520c445632d9a6232c6fe18c3a7f))

### Bug Fixes

* **deps:** 降级 conventional-changelog-conventionalcommits 至 v8 以兼容 semantic-release ([df5348a](https://github.com/pandaymx/lanchat/commit/df5348aafdd7690fb734f54cdae36fcbcba795f9))
* **repo:** 钩子命令显式导出 PATH 以兼容 SSH push 子进程 ([910f211](https://github.com/pandaymx/lanchat/commit/910f211e36bc421fca7185c8bc38d86efeaf99dd))
* **repo:** 移除方案文档与所有引用 ([ec7c870](https://github.com/pandaymx/lanchat/commit/ec7c87038869f6fea6b97a78fc39dae1f1218ab3))
