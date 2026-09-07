#!/usr/bin/env bash
# LAN Chat Linux .deb 打包（desktop + CLI 通用，M11.1）
# 用法（cwd = repo root）：pkg-linux-deb.sh <version> <arch> <bin> [kind]
#   version 形如 0.9.0（无 v 前缀，dpkg 版本格式）
#   arch    形如 amd64 / arm64
#   bin     desktop / hub / tui / web
#   kind    desktop（默认，带 .desktop + GTK 依赖）| cli（纯 Go 静态无依赖）
# 产物：dist/lanchat-<bin>-<version>-linux-<arch>.deb（哈希由 checksums job 汇总）
set -euo pipefail

VERSION="${1:?usage: pkg-linux-deb.sh <version> <arch> <bin> [kind]}"
ARCH="${2:?}"
BIN="${3:?}"
KIND="${4:-desktop}"
[ "$BIN" = "desktop" ] && KIND="desktop" || KIND="${4:-cli}"

NAME="lanchat-${BIN}-${VERSION}-linux-${ARCH}.deb"

stage="dist/deb-root"
rm -rf "$stage"
mkdir -p "$stage/DEBIAN" "$stage/usr/bin"

install -m 0755 "dist/raw/${BIN}" "$stage/usr/bin/${BIN}"

# control：desktop 的 Depends 覆盖 Wails v3 运行时（gtk4 + webkitgtk-6.0 +
# libsoup3），apt 安装时自动拉依赖；CLI 纯 Go 静态二进制零依赖。
if [ "$KIND" = "desktop" ]; then
  DEPENDS="libgtk-4-1, libwebkitgtk-6.0-4, libsoup-3.0-0, libglib2.0-0"
  mkdir -p "$stage/usr/share/applications"
  cat > "$stage/usr/share/applications/lanchat.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=LAN Chat
Comment=Chat over LAN
Exec=lanchat-desktop
Terminal=false
Categories=Network;InstantMessaging;
EOF
else
  DEPENDS=""
fi

{
  echo "Package: lanchat-${BIN}"
  echo "Version: ${VERSION}"
  echo "Section: net"
  echo "Priority: optional"
  echo "Architecture: ${ARCH}"
  echo "Maintainer: pandaymx <dev@pandaymx>"
  [ -z "$DEPENDS" ] || echo "Depends: ${DEPENDS}"
  echo "Description: LAN Chat ${BIN}"
  echo " Chat over LAN without internet."
} > "$stage/DEBIAN/control"

dpkg-deb --build --root-owner-group "$stage" "dist/$NAME"
echo "built dist/$NAME"
