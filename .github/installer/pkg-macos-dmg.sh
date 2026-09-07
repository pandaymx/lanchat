#!/usr/bin/env bash
# LAN Chat 桌面端 macOS .dmg 打包（M10.3）
# 用法（cwd = repo root）：pkg-macos-dmg.sh <version> <arch>
#   version 形如 0.9.0
#   arch    形如 arm64 / amd64（macos-latest 原生 GOARCH）
# 产物：dist/lanchat-desktop-<version>-darwin-<arch>.dmg（哈希由 checksums job 汇总）
# 说明：ad-hoc 签名（codesign -s -）保证本机可直接运行；无开发者证书，
# 不做 Apple 公证（Gatekeeper 首次打开右键->打开）。
set -euo pipefail

VERSION="${1:?usage: pkg-macos-dmg.sh <version> <arch>}"
ARCH="${2:?usage: pkg-macos-dmg.sh <version> <arch>}"
NAME="lanchat-desktop-${VERSION}-darwin-${ARCH}.dmg"

stage="dist/dmg-root"
rm -rf "$stage"
mkdir -p "$stage/LAN Chat.app/Contents/MacOS"
install -m 0755 dist/raw/lanchat-desktop "$stage/LAN Chat.app/Contents/MacOS/lanchat-desktop"

cat > "$stage/LAN Chat.app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>LAN Chat</string>
  <key>CFBundleDisplayName</key><string>LAN Chat</string>
  <key>CFBundleIdentifier</key><string>com.pandaymx.lanchat.desktop</string>
  <key>CFBundleVersion</key><string>VERSION_PLACEHOLDER</string>
  <key>CFBundleShortVersionString</key><string>VERSION_PLACEHOLDER</string>
  <key>CFBundleExecutable</key><string>lanchat-desktop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
sed -i '' "s/VERSION_PLACEHOLDER/${VERSION}/g" "$stage/LAN Chat.app/Contents/Info.plist"

# ad-hoc 签名：无证书时让 app 在本机可直接运行（未签名会被 Gatekeeper 拦）。
codesign --force --deep -s - "$stage/LAN Chat.app"

# /Applications 软链 → dmg 内出现 Applications 目录，用户拖拽安装。
ln -s /Applications "$stage/Applications"

hdiutil create -volname "LAN Chat" -srcfolder "$stage" -ov -format UDZO "dist/$NAME"
echo "built dist/$NAME"