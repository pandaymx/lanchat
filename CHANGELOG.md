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
