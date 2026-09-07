#!/usr/bin/env bash
# LAN Chat Windows NSIS 安装包（desktop + CLI 通用，M11.1）
# 用法（cwd = repo root）：pkg-windows-nsis.sh <version> <arch> <bin>
#   version 形如 0.9.0
#   arch    形如 amd64 / arm64
#   bin     desktop / hub / tui / web
# 依赖：makensis（Linux: apt install nsis / Windows: choco install nsis；
#       Windows runner 上 choco 装的 NSIS 不在 PATH，用 MAKENSIS 环境变量
#       指定全路径，如 /c/Program Files (x86)/NSIS/makensis.exe）
# 产物：dist/lanchat-<bin>-<version>-windows-<arch>-setup.exe
set -euo pipefail

VERSION="${1:?usage: pkg-windows-nsis.sh <version> <arch> <bin>}"
ARCH="${2:?}"
BIN="${3:?}"

case "$BIN" in
  desktop) APPNAME="LAN Chat";      EXE="lanchat-desktop.exe" ;;
  hub)     APPNAME="LAN Chat Hub";  EXE="hub.exe" ;;
  tui)     APPNAME="LAN Chat TUI";  EXE="tui.exe" ;;
  web)     APPNAME="LAN Chat Web";  EXE="web.exe" ;;
  *) echo "unknown bin: $BIN" >&2; exit 1 ;;
esac

NAME="lanchat-${BIN}-${VERSION}-windows-${ARCH}-setup.exe"
MAKENSIS="${MAKENSIS:-makensis}"

# Windows 的 MSYS 会把 /Dxxx=value 参数做路径转换（DNAME 变 C:/Program
# Files/Git/DNAME），必须关掉；Linux 无此问题。Linux 的 makensis 只认 -D
# 前缀（/D 会被当成脚本路径），Windows 的 makensis 两种都认，统一用 -D。
( cd dist/raw && MSYS_NO_PATHCONV=1 "$MAKENSIS" \
    -DVERSION="${VERSION}" -DAPPNAME="${APPNAME}" -DEXE="${EXE}" \
    -DOUTFILE="../../dist/${NAME}" ../../.github/installer/lanchat.nsi )
echo "built dist/${NAME}"
