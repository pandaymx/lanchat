# Changelog

## [0.6.0](https://github.com/pandaymx/lanchat/compare/v0.5.0...v0.6.0) (2026-09-29)


### Features

* **ui-win:** M4 Windows native client ([#11](https://github.com/pandaymx/lanchat/issues/11)) ([f381338](https://github.com/pandaymx/lanchat/commit/f3813386f80e67f7881d9335be0cf3d9879cba07))

## [0.5.0](https://github.com/pandaymx/lanchat/compare/v0.4.0...v0.5.0) (2026-09-29)


### Features

* **core:** M4 客户端核心、daemon 进程与 JSON-RPC IPC ([#8](https://github.com/pandaymx/lanchat/issues/8)) ([afc5728](https://github.com/pandaymx/lanchat/commit/afc5728ad4773eda266c4a9fd7357713b02b737a))


### Bug Fixes

* **core:** 修正 TestPeerRoster 等待错误对象导致的偶发失败 ([#10](https://github.com/pandaymx/lanchat/issues/10)) ([d58cd2d](https://github.com/pandaymx/lanchat/commit/d58cd2df451da98c3f3c1258ca572fae06f56b95))

## [0.4.0](https://github.com/pandaymx/lanchat/compare/v0.3.0...v0.4.0) (2026-09-28)


### Features

* **core:** M3 可靠性增强与 AES-GCM 中继 ([#6](https://github.com/pandaymx/lanchat/issues/6)) ([2276683](https://github.com/pandaymx/lanchat/commit/2276683dcd26a4a6de486559356efad45bdc6606))

## [0.3.0](https://github.com/pandaymx/lanchat/compare/v0.2.0...v0.3.0) (2026-09-27)


### Features

* M2 mDNS 零配置发现 + P2P 主干传输 ([#3](https://github.com/pandaymx/lanchat/issues/3)) ([23a3628](https://github.com/pandaymx/lanchat/commit/23a3628518ab276ec5c87237ace3500bba01e2ac))

## [0.2.0](https://github.com/pandaymx/lanchat/compare/lanchat-v0.1.1...lanchat-v0.2.0) (2026-09-27)


### Features

* **config:** add server config loader with flag env yaml precedence ([1f9e51b](https://github.com/pandaymx/lanchat/commit/1f9e51b10d5cba9ab84dec124086a0ac15eb48b5))
* **core:** wire serve genpsk and version subcommands to server ([2707559](https://github.com/pandaymx/lanchat/commit/27075594878cc1fe046e4be143c45331a6a71e91))
* **server:** add signaling hub registry router with psk heartbeat and text ([1be9137](https://github.com/pandaymx/lanchat/commit/1be9137b9c973dec05919f6ba8474778d8826e1b))

## [0.1.1](https://github.com/pandaymx/lanchat/compare/lanchat-v0.1.0...lanchat-v0.1.1) (2026-09-27)


### Features

* **appapi:** add appapi stubs, ipc schema v1 and go ci ([5cc79e0](https://github.com/pandaymx/lanchat/commit/5cc79e091f30f560d07591f51a19566991151a88))
* **protocol:** add envelope and control message types ([ec892a6](https://github.com/pandaymx/lanchat/commit/ec892a63a921fd323943d95b3fbdb057ba48d564))
* **protocol:** add lctp frame, framer and fuzz tests ([169fd17](https://github.com/pandaymx/lanchat/commit/169fd170f4d76986ac6eb5c4dc96c98cb4e5fdcf))
* **protocol:** add protocol version constants ([aa5bad0](https://github.com/pandaymx/lanchat/commit/aa5bad0049d5b759d6b8cf7ffa337de6a62344a7))
