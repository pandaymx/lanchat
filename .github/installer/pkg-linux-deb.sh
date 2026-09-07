#!/usr/bin/env bash
# LAN Chat 桌面端 Linux .deb 打包（M10.3）
# 用法（cwd = repo root）：pkg-linux-deb.sh <version> <arch>
#   version 形如 0.9.0（无 v 前缀，dpkg 版本格式）
#   arch    形如 amd64 / arm64
# 产物：dist/lanchat-desktop-<version>-linux-<arch>.deb（哈希由 checksums job 汇总）
set -euo pipefail

VERSION="${1:?usage: pkg-linux-deb.sh <version> <arch>}"
ARCH="${2:?usage: pkg-linux-deb.sh <version> <arch>}"
NAME="lanchat-desktop-${VERSION}-linux-${ARCH}.deb"

stage="dist/deb-root"
rm -rf "$stage"
mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/share/applications"

install -m 0755 dist/raw/lanchat-desktop "$stage/usr/bin/lanchat-desktop"

# control：Depends 覆盖 Wails v3 运行时（gtk4 + webkitgtk-6.0 + libsoup3），
# apt 安装时自动拉依赖——这是安装包相对 zip 的核心价值。
cat > "$stage/DEBIAN/control" <<EOF
Package: lanchat-desktop
Version: ${VERSION}
Section: net
Priority: optional
Architecture: ${ARCH}
Maintainer: pandaymx <dev@pandaymx>
Depends: libgtk-4-1, libwebkitgtk-6.0-4, libsoup-3.0-0, libglib2.0-0
Description: LAN Chat desktop client
 Chat over LAN without internet. Local webui served on 127.0.0.1 and
 rendered in a Wails v3 window (ADR-015).
EOF

cat > "$stage/usr/share/applications/lanchat.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=LAN Chat
Comment=Chat over LAN
Exec=lanchat-desktop
Terminal=false
Categories=Network;InstantMessaging;
EOF

dpkg-deb --build --root-owner-group "$stage" "dist/$NAME"
echo "built dist/$NAME"