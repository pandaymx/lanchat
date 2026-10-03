#!/usr/bin/env bash
# generate-release-notes.sh — 生成 Release Notes body（Markdown）。
#
# 输出到 stdout。包含：
#   1. CHANGELOG.md 中对应版本的条目
#   2. 安全 / 签名警告（SmartScreen、Gatekeeper）
#   3. 内网安装指引
#
# 环境变量：
#   TAG — 发布 tag（如 v0.18.0）
#   UNSIGNED — 若为 "true" 则在标题追加 "（未签名）"
set -euo pipefail

TAG="${TAG:?需要 TAG 环境变量}"
VERSION="${TAG#v}"
UNSIGNED="${UNSIGNED:-false}"

cat <<HEADER
## LANChat ${TAG}
HEADER

if [[ "$UNSIGNED" == "true" ]]; then
  echo ""
  echo "> **⚠ 未签名构建** — 所有平台产物均未经代码签名。"
  echo "> 首次打开时系统可能弹出安全警告，请按下方指引操作。"
fi

# ─── 安全提示 ─────────────────────────────────────────────────────
cat <<'SECURITY'

### 安全提示

<details>
<summary>Windows SmartScreen 拦截</summary>

- MSI / EXE 未经 Microsoft 签名，SmartScreen 会显示「Windows 已保护你的电脑」
- 点击 **更多信息** → **仍要运行** 即可
- 便携版 ZIP 无安装步骤，解压即用，不受 SmartScreen 影响

</details>

<details>
<summary>macOS Gatekeeper 拦截</summary>

- App 未经 Apple 公证（notarize），首次打开会提示「已损坏」或「无法验证开发者」
- 解决方法：
  1. 在 Finder 中右键点击 App → **打开**
  2. 弹窗中点击 **打开**
  3. 或终端执行：`xattr -cr /Applications/LANChat.app`

</details>

<details>
<summary>Android 安装</summary>

- 若为 debug APK（未配置签名密钥），安装前需允许「安装未知来源应用」
- Release 签名 APK 无需额外设置

</details>

SECURITY

# ─── 内网安装指引 ─────────────────────────────────────────────────
cat <<'INSTALL'

### 内网安装指引

1. 从本页面下载对应平台的安装包
2. **Windows**：推荐 MSI 安装器；免安装可选 portable ZIP
3. **macOS**：解压 ZIP 后拖入 `/Applications`，首次打开需右键放行
4. **Linux**：解压 tar.gz，`./lanchat-ui` 启动（需系统有 GTK4 + libadwaita）
5. **Android**：APK 直接安装；首次启动需授予局域网权限
6. **iOS**：Simulator 包仅供开发调试，真机需自行签名
7. 所有平台首次使用需在同一局域网内，输入相同的服务端地址和口令即可互联

INSTALL

# ─── CHANGELOG ────────────────────────────────────────────────────
CHANGELOG="CHANGELOG.md"
if [[ -f "$CHANGELOG" ]]; then
  # 提取 CHANGELOG 中当前版本的条目（从 ## [VERSION] 到下一个 ## 之间）
  NOTES=$(awk -v ver="$VERSION" '
    /^## \[/ {
      if (found) exit
      # 匹配 ## [0.18.0] 或 ## [0.18.0] (2026-10-03) 格式
      gsub(/[\[\]]/, "", $0)
      split($0, a, " ")
      if (a[2] == ver) { found = 1; next }
    }
    found { print }
  ' "$CHANGELOG")

  if [[ -n "$NOTES" ]]; then
    echo "### 变更内容"
    echo ""
    echo "$NOTES"
  else
    echo "### 变更内容"
    echo ""
    echo "详见 [CHANGELOG.md](https://github.com/pandaymx/lanchat/blob/main/CHANGELOG.md)"
  fi
fi

# ─── 制品清单 ─────────────────────────────────────────────────────
echo ""
echo "### 制品清单"
echo ""
echo "| 平台 | 文件 | 说明 |"
echo "|------|------|------|"
echo "| Windows | \`.msi\` | 安装器（推荐） |"
echo "| Windows | \`-portable.zip\` | 免安装便携版 |"
echo "| Windows | \`-Store.msix\` | Microsoft Store 上传包 |"
echo "| Windows | \`-Sideload.msix\` | 侧载包 + 证书 |"
echo "| macOS | \`-macos-universal-unsigned.zip\` | Universal 二进制 |"
echo "| Linux | \`-linux-x64.tar.gz\` | GTK4 + libadwaita |"
echo "| Android | \`-android-*.apk\` | release 或 debug |"
echo "| iOS | \`-ios-simulator-unsigned.zip\` | Simulator only |"
echo "| Daemon | \`lanchat-daemon-*-*.exe/.gz\` | 独立守护进程 |"
