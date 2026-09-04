# LAN Chat

局域网即时通讯，Go 优先，面向程序员用户，覆盖 **TUI / Web / 桌面 / 移动** 四端。

> 当前状态：**M0 工程地基**。还没有业务代码，先把工具链和规范立起来。

## 快速开始

```bash
# 1. 装 git 钩子（clone 后必做）
lefthook install

# 2. 装 Node 侧依赖（commitlint + semantic-release）
bun install

# 3. 构建（版本号从 git tag 注入）
make build
```

## 工具链分工

| 领域 | 工具 |
|---|---|
| Go 构建 / 测试 | `make`（见 Makefile） |
| Go 格式化 | `gofumpt` + `gci` + `templ fmt` |
| Go 静态检查 | `golangci-lint` |
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
```

scope 只能是：`core` `proto` `hub` `tui` `web` `desktop` `mobile` `store` `deps` `ci` `docs` `repo` `release`

详细的提交规则见 [`AGENTS.md`](./AGENTS.md) §5。

## 版本发布

版本号、CHANGELOG、git tag 全部由 semantic-release 自动管理，**不要手动改版本号或打 tag**。

```bash
bun run release:dry   # 预演，确认算出的版本号与 notes
bun run release       # 正式发版
```

版本号不写入源码，构建时通过 ldflags 注入，`lanchat --version` 读的是 git tag。

## 环境要求

- Go 1.27+
- bun（`~/.bun/bin`，需加入 PATH）
- `gofumpt` `gci` `templ` `golangci-lint` `lefthook`（`go install` 装）
- WSL2 需开启 `networkingMode=Mirrored`，否则局域网其它设备访问不到服务
