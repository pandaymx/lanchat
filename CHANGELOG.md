## [1.0.0](https://github.com/pandaymx/lanchat/compare/v0.13.1...v1.0.0) (2026-09-08)

### ⚠ BREAKING CHANGES

* **proto:** 群聊五帧与 Typing.ConversationID 是破坏性协议
变更，v0.x 客户端不兼容，需随 v1.0.0 一并升级。

### Features

* **hub:** M12-A 群聊协议与路由（FKConvList/Create/Invite/Leave + 会话广播收敛 + 大厅空会话） ([6252a7e](https://github.com/pandaymx/lanchat/commit/6252a7eafbbbd8dc6c9afb9fdecc0378aeb333bb))
* **tui:** M12-A 群聊（会话列表/切换/建群/邀请/退群 + 按会话过滤） ([561f558](https://github.com/pandaymx/lanchat/commit/561f558dd993a3449e88ca96b1f1c7224597a257))
* **web:** M12-A 多会话 Web UI（会话侧栏/建群/退群/按会话分流 SSE） ([d34109f](https://github.com/pandaymx/lanchat/commit/d34109f76d5492b597e2a6d03cc524fe57e1c18d))

### Bug Fixes

* **release:** Windows NSIS 改反斜杠绝对路径 + 产物诊断 ([0e738d7](https://github.com/pandaymx/lanchat/commit/0e738d789227c545ef24eae33127c2473e6cc7ab))
* **repo:** 修 M12-A 引入的 13 处 lint 问题 ([86a4a2f](https://github.com/pandaymx/lanchat/commit/86a4a2f1791f6ecc7e82a2ab661a0823d311e872))

### Code Refactoring

* **proto:** 记录 M12 群聊协议破坏性变更（文档） ([3dd5850](https://github.com/pandaymx/lanchat/commit/3dd58504e63b8c562f5f179285d8a597c4610168))

## [0.13.1](https://github.com/pandaymx/lanchat/compare/v0.13.0...v0.13.1) (2026-09-07)

### Bug Fixes

* **release:** Windows NSIS 路径转 cygpath；package job cd 回仓库根；deb/rpm 修 desktop 产物名 ([fbe17f0](https://github.com/pandaymx/lanchat/commit/fbe17f0c229473e4f77004849d679191908c347c))

## [0.13.0](https://github.com/pandaymx/lanchat/compare/v0.12.0...v0.13.0) (2026-09-07)

### Features

* **release:** 安装包矩阵扩展到全平台全架构 ([0d678cb](https://github.com/pandaymx/lanchat/commit/0d678cbac33447e9874438a7677a2d1d13815594))

### Bug Fixes

* **release:** nsis wrapper 提前捕获仓库根路径（子 shell cd 后 pwd 失效） ([6863ba4](https://github.com/pandaymx/lanchat/commit/6863ba4156c88705561e26ff4d26b6e8fa81b310))
* **release:** rpm %install 建 buildroot/usr/bin；nsis File/OutFile 传绝对路径 ([5df4f13](https://github.com/pandaymx/lanchat/commit/5df4f130cc95d877ceb75acd6d64d502456cdc4c))
* **release:** 安装包脚本三处修复（deb 空依赖空行 / nsis -D 前缀 / rpm spec 生成与架构映射） ([6132085](https://github.com/pandaymx/lanchat/commit/61320853758931b12804898fbb6546850e0cb904))
* **release:** 安装包脚本补可执行位（Windows 写入丢 mode） ([7968351](https://github.com/pandaymx/lanchat/commit/7968351d1b0f94aa7813a2c76a29f54149d538d8))
* **release:** 移除 nsi 冗余 !cd；验证版本号去 - 兼容 rpm ([5356fec](https://github.com/pandaymx/lanchat/commit/5356fec47936a817f852163ea339516c7feb0850))

## [0.12.0](https://github.com/pandaymx/lanchat/compare/v0.11.0...v0.12.0) (2026-09-07)

### Features

* **release:** Windows 各端嵌入应用图标与 asInvoker manifest ([22649dd](https://github.com/pandaymx/lanchat/commit/22649dde6170eeabe63cbc98c6d8e31c4bf902a0))

## [0.11.0](https://github.com/pandaymx/lanchat/compare/v0.10.1...v0.11.0) (2026-09-07)

### Features

* **web:** M11 三栏 IM 布局重构 + 浅深双主题 ([5169a7a](https://github.com/pandaymx/lanchat/commit/5169a7aa99a3b05be2f85fb91407451b4501441e))

## [0.10.1](https://github.com/pandaymx/lanchat/compare/v0.10.0...v0.10.1) (2026-09-07)

### Bug Fixes

* **desktop:** M10.2 编译错误（msg.User → SenderUserID、float64 截断） ([8b9fc63](https://github.com/pandaymx/lanchat/commit/8b9fc63039a7d93a08520a2332505d29d244555f))
* **repo:** mDNS 失败时回退探测本机回环 hub（同机开箱即用） ([eecc939](https://github.com/pandaymx/lanchat/commit/eecc93902abccb52c5ff3a2d5bc20a1cb6376c0b))

## [0.10.0](https://github.com/pandaymx/lanchat/compare/v0.9.3...v0.10.0) (2026-09-07)

### Features

* **desktop:** M10.2 托盘与系统通知 ([b1765b0](https://github.com/pandaymx/lanchat/commit/b1765b0de820b25ced0d994bcacb5d59bacb035d))

## [0.9.3](https://github.com/pandaymx/lanchat/compare/v0.9.2...v0.9.3) (2026-09-07)

### Bug Fixes

* **ci:** NSIS File 指令先 !cd 到二进制目录 ([b69a0d0](https://github.com/pandaymx/lanchat/commit/b69a0d01f59ece17ea6efdc244ab71b4655f0974))

## [0.9.2](https://github.com/pandaymx/lanchat/compare/v0.9.1...v0.9.2) (2026-09-07)

### Bug Fixes

* **ci:** NSIS 参数禁用 MSYS 路径转换 ([b861b95](https://github.com/pandaymx/lanchat/commit/b861b95fd5fcf26a009762e4ca6ca4a58ddd8804))

## [0.9.1](https://github.com/pandaymx/lanchat/compare/v0.9.0...v0.9.1) (2026-09-07)

### Bug Fixes

* **ci:** windows NSIS 全路径调用 ([fd913c0](https://github.com/pandaymx/lanchat/commit/fd913c0738f37f73532a1f392c37cd7d833e8570))

## [0.9.0](https://github.com/pandaymx/lanchat/compare/v0.8.4...v0.9.0) (2026-09-07)

### Features

* **desktop:** 桌面端三平台安装包（NSIS / dmg / deb） ([8bbff35](https://github.com/pandaymx/lanchat/commit/8bbff3580aa6d4cd578315cc74b4bd86ab829925))

## [0.8.4](https://github.com/pandaymx/lanchat/compare/v0.8.3...v0.8.4) (2026-09-07)

### Bug Fixes

* **ci:** windows 桌面产物显式加 .exe ([c72fff9](https://github.com/pandaymx/lanchat/commit/c72fff9bbbbd9d913135bedaffa4c389e6fba456))

## [0.8.3](https://github.com/pandaymx/lanchat/compare/v0.8.2...v0.8.3) (2026-09-07)

### Bug Fixes

* **ci:** windows 桌面打包改 PowerShell Compress-Archive ([deb99be](https://github.com/pandaymx/lanchat/commit/deb99beaa081402f6793a7abf8f65a480534305e))

## [0.8.2](https://github.com/pandaymx/lanchat/compare/v0.8.1...v0.8.2) (2026-09-07)

### Bug Fixes

* **ci:** 桌面 job 打包路径与 windows zip 命令 ([d0ec631](https://github.com/pandaymx/lanchat/commit/d0ec631f10c8211079a2239c42c701c3b52da52f))

## [0.8.1](https://github.com/pandaymx/lanchat/compare/v0.8.0...v0.8.1) (2026-09-07)

### Bug Fixes

* **desktop:** Wails v3 beta.17 窗口 API 改为包级 NewWindow ([47c43d7](https://github.com/pandaymx/lanchat/commit/47c43d7a2bc96bc8e3be8cb8ae85de476b68d528))

## [0.8.0](https://github.com/pandaymx/lanchat/compare/v0.7.0...v0.8.0) (2026-09-07)

### Features

* **desktop:** Wails v3 窗口壳（//go:build desktop CGO 隔离） ([c9e7489](https://github.com/pandaymx/lanchat/commit/c9e7489e3931a15677d27cc0511a14dfcde23e2c))
* **desktop:** 本地 webui 装配（回环随机端口 + 可编程生命周期） ([49f1ef3](https://github.com/pandaymx/lanchat/commit/49f1ef3742838f34e9b4c477e14526843ce0b2a3))

## [0.7.0](https://github.com/pandaymx/lanchat/compare/v0.6.1...v0.7.0) (2026-09-07)

### Features

* **core:** client 文件上传/下载 API ([bd62f54](https://github.com/pandaymx/lanchat/commit/bd62f543dd383ba5e49942f07be2901825da79db))
* **hub:** 文件上传/下载端点 + blob 存储服务 ([ce80ec7](https://github.com/pandaymx/lanchat/commit/ce80ec7c93fc99e1da3c4c86a2027314f8815c5d))
* **proto:** StoredMessage 增加 FileRef 附件字段 ([1ef9ad3](https://github.com/pandaymx/lanchat/commit/1ef9ad3960048fcc2b265ba08590a3c0e78cf8c9))
* **store:** 文件元信息存取 + messages 附件列迁移 ([dfad170](https://github.com/pandaymx/lanchat/commit/dfad170fd8318abdbb0e34fb8481520b009ad8f4))
* **tui:** /file 命令 + 附件卡片 + 自动下载 ([3fa8f77](https://github.com/pandaymx/lanchat/commit/3fa8f77de25bf5495a14b10042396927f42bcfc3))
* **web:** 文件上传/下载代理 + 附件卡片 + 图片内联预览 ([609015c](https://github.com/pandaymx/lanchat/commit/609015c7164d1b5db320f72e671104ace7117858))

### Bug Fixes

* **hub:** golangci-lint 0 issues（bodyclose/revive/staticcheck） ([e4c9685](https://github.com/pandaymx/lanchat/commit/e4c9685ebdec6fb16ac07d19fba39378066af194))

## [0.6.1](https://github.com/pandaymx/lanchat/compare/v0.6.0...v0.6.1) (2026-09-07)

### Bug Fixes

* **tui:** markdown 测试时间串断言不依赖本地时区 ([1dfed85](https://github.com/pandaymx/lanchat/commit/1dfed851bad22d75952c4efd17b83ec62a3578ca))

## [0.6.0](https://github.com/pandaymx/lanchat/compare/v0.5.0...v0.6.0) (2026-09-07)

### Features

* **repo:** M8.1 已读回执端到端 + M8.2 Markdown/代码高亮 ([7323a65](https://github.com/pandaymx/lanchat/commit/7323a65780674c7031bd3caecf040834778948b6))

### Bug Fixes

* **deps:** 修复 CI 发版链路（锁定 conventionalcommits ^8 + git 凭据 insteadOf） ([13f7fb3](https://github.com/pandaymx/lanchat/commit/13f7fb3539034b0dc72aa60f8d98d068bcf8cc2d))
* **repo:** 修复 golangci-lint 告警（M8 引入，pre-push 拦截） ([afe29d8](https://github.com/pandaymx/lanchat/commit/afe29d8445686e7ffa61df3cb9e6e918c95f48c5))

## [0.5.0](https://github.com/pandaymx/lanchat/compare/v0.4.0...v0.5.0) (2026-09-06)

### Features

* **core:** Client 收发 typing——SendTyping 与 Typing() 快照 ([f2d59ac](https://github.com/pandaymx/lanchat/commit/f2d59ace1653c139ac9ff3469827afc1ab812805))
* **core:** Client 维护在线名单快照并发布 EventPresence ([ac6255d](https://github.com/pandaymx/lanchat/commit/ac6255d7a8e438a2e1e26ae9ea3c235eecb4a9de))
* **hub:** 在线状态广播——握手发 roster、上下线广播 FKPresence ([7ee980e](https://github.com/pandaymx/lanchat/commit/7ee980e54dbe9470d0fe880e5abc4cad6135df99))
* **hub:** typing 帧按注册表盖戳身份并广播 ([93048e0](https://github.com/pandaymx/lanchat/commit/93048e0d736b27de94136889ff8d4183aa06f06a))
* **proto:** Typing 帧负载结构与 core.Event.Typing 字段 ([fb5e281](https://github.com/pandaymx/lanchat/commit/fb5e28104b5ab66e8f0de5ee9b1e597dc3bb39b2))
* **tui:** 正在输入指示——输入节流上发与 hints 行状态 ([7487d5f](https://github.com/pandaymx/lanchat/commit/7487d5f29481c5642aba8b2120785a2af3a8d85c))
* **web:** 在线成员条——首屏 roster 渲染 + presence SSE 实时刷新 ([576200f](https://github.com/pandaymx/lanchat/commit/576200f30fb337699d7056454438d228e58eb115)), closes [#peers](https://github.com/pandaymx/lanchat/issues/peers)
* **web:** 正在输入条——/typing 端点与 typing SSE 帧 ([af4a02a](https://github.com/pandaymx/lanchat/commit/af4a02a1134f98b4c7705d137f84adc0c747708d))

### Bug Fixes

* **repo:** commitlint 钩子改用 bun 直接运行，修非交互 shell 找不到 node ([99e577c](https://github.com/pandaymx/lanchat/commit/99e577cb0e4d55e7788f823887e09358b05a8129))

## [0.4.0](https://github.com/pandaymx/lanchat/compare/v0.3.0...v0.4.0) (2026-09-06)

### Features

* **core:** Client.FetchHistory 同步拉取历史分页 ([07d4dbe](https://github.com/pandaymx/lanchat/commit/07d4dbe502887dcf817b74d8c8048ca67802bde3))
* **deps:** 引入 templ v0.3.1020 + vendored htmx 1.9.12（含 SSE 扩展） ([007a173](https://github.com/pandaymx/lanchat/commit/007a173ac0a1def5da5b8bebf39f645227d1372b))
* **hub:** 新增 internal/discovery mDNS/DNS-SD 服务发现（M6.1） ([00e45de](https://github.com/pandaymx/lanchat/commit/00e45de7bae8db670ad3b97a8cd5084c80d11521))
* **hub:** cmd/hub 接入 libSQL 持久化与重启恢复（M5.2） ([2d44610](https://github.com/pandaymx/lanchat/commit/2d446102224e9da92624ae8838645d400036330e))
* **hub:** History.Query 支持 Before 向更早翻页 ([a5d1cc9](https://github.com/pandaymx/lanchat/commit/a5d1cc97450fbaef55d994f525aa092f11a3cd55))
* **hub:** hub mDNS 广播与 tui/web 自动发现接线（M6.2） ([a50f3a6](https://github.com/pandaymx/lanchat/commit/a50f3a6ad7f87db0bda605d38a3cf0ce7d3ce158))
* **proto:** HistoryRequest 加 Before 字段支持向更早翻页 ([1a09ec6](https://github.com/pandaymx/lanchat/commit/1a09ec6f0b08891cd42208c03605ff6af3b6c5f0))
* **repo:** 接入 slog 日志系统（pkg/logging + 全链路埋点 + flag） ([f5b7fd1](https://github.com/pandaymx/lanchat/commit/f5b7fd1091b3e56077c386809b61aee2413526c7))
* **store:** 新增 pkg/store/libsql 持久化实现（ADR-013） ([f631656](https://github.com/pandaymx/lanchat/commit/f6316562f428146c7205d9120bad93dd4bc84ebf))
* **tui:** 接入 Translator 接口，14 处 UI 文案走 i18n ([475f9e7](https://github.com/pandaymx/lanchat/commit/475f9e7bcc4163369308a3147ced23542c3dc21d))
* **tui:** 上翻到顶自动加载更早历史（M7.1） ([27b435d](https://github.com/pandaymx/lanchat/commit/27b435d28fe93464748a0aac7f874b19acd32ee9))
* **tui:** 新增 internal/i18n 包 + embed bundles/en + bundles/zh-CN + 全套测试 ([6fa8af4](https://github.com/pandaymx/lanchat/commit/6fa8af4b7cbbc4cb47859daad34b969ca825d2d2))
* **tui:** cmd/tui 加 -lang / -lang-list flag，启动期加载 i18n bundle 注入 Model ([445c390](https://github.com/pandaymx/lanchat/commit/445c390cd6696b33f66916cefd85faea5225393e))
* **web:** 断连 banner——SSE state 帧 swap 连接状态区 ([8d8291e](https://github.com/pandaymx/lanchat/commit/8d8291ecd110891e76ae34eba2b29e4ab8f6cb4c)), closes [#conn-state](https://github.com/pandaymx/lanchat/issues/conn-state)
* **web:** 历史「加载更早消息」分页（/history 端点） ([c1c1ff8](https://github.com/pandaymx/lanchat/commit/c1c1ff8208deba9ebb1ddd878411488d0f6cc4cc))
* **web:** 输入框 Enter 发送、Shift+Enter 换行 ([3b1bd56](https://github.com/pandaymx/lanchat/commit/3b1bd56269c04dfefd1333f25ccc10bf5572e302))
* **web:** cookie 签发与 Session Manager 复用/TTL 回收 ([a792f82](https://github.com/pandaymx/lanchat/commit/a792f821564ae7a7b0eea09406f7c41630049304))
* **web:** DialClient 装配 pkg/client 拨号与握手 ([3e635a0](https://github.com/pandaymx/lanchat/commit/3e635a01b3ba037b2edde4183a70c673d3ebad07))
* **web:** handler 注入 Client——SSE 推流 + 发消息转发 + 首页历史 ([70f4269](https://github.com/pandaymx/lanchat/commit/70f426964e9fc4483e733ef841c78649cfa8a9d8))
* **web:** Last-Event-ID 断线重连补发（SSE catch-up） ([6c7ae15](https://github.com/pandaymx/lanchat/commit/6c7ae15ba7ab3910f519105e1cc7c0d791b6b34d))
* **web:** M4.2 骨架 —— cmd/web 起 :9001 + 三路由占位 + templ 模板 ([918fc53](https://github.com/pandaymx/lanchat/commit/918fc53e42edf91379ba8b1e2a43f1124c56142e))
* **web:** Session fanout 多 SSEWriter，handler 接 Manager 惰性拨号 ([e50e221](https://github.com/pandaymx/lanchat/commit/e50e2214478482fa635ed4da0ca2f74de260bb57))
* **web:** Web 端 i18n（复用 internal/i18n bundle，-lang flag） ([126f541](https://github.com/pandaymx/lanchat/commit/126f5410ffb1738769d1c5a116b26989b8a1ce95))

### Bug Fixes

* **repo:** awaitReady 屏障修复 race+cover 下 FKHello 异步竞争致 Bob 漏收 FKDeliver ([ab3efb0](https://github.com/pandaymx/lanchat/commit/ab3efb0af26b335b7ca6617c6b3786c2c11ecc9d))
* **repo:** Client 起内部 pumpCtx 隔离 readPump 与 dialCtx ([0f0225f](https://github.com/pandaymx/lanchat/commit/0f0225fc3d62e9fa345c147c51a55c95abfd62a2))
* **repo:** Makefile fmt 的 gci 补 sections 参数与钩子对齐 ([25b11ce](https://github.com/pandaymx/lanchat/commit/25b11cef5d76c2b153e0b61519aefc7f025e2f2f))
* **store:** 补 History rows.Close 的 errcheck 忽略 ([de53143](https://github.com/pandaymx/lanchat/commit/de5314390b31d4b654f741d5ca618321ae5e2958))
* **tui:** 关闭 textarea virtual cursor，让 Windows 中文输入法候选框跟随输入位置 ([c8211a7](https://github.com/pandaymx/lanchat/commit/c8211a75115568dfc87ebe5be66b7e76a81728bc))
* **tui:** 小键盘 Enter 也路由到 trySubmitInput / InsertNewline ([3f84983](https://github.com/pandaymx/lanchat/commit/3f84983f3ccbb2049071b09e536c15affa0c6983))
* **tui:** bundle 内部 locale 一律 lowercase，避免 zh-CN ↔ zh-cn 静默回退 ([e56e7db](https://github.com/pandaymx/lanchat/commit/e56e7db8d7478f28eb8f05218b104d14c46e6553))

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
