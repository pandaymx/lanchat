#!/usr/bin/env bash
# LAN Chat Windows MSI 安装包（desktop + CLI 通用）。
# 用法（cwd = repo root）：pkg-windows-msi.sh <version> <arch> <bin>
#   version 形如 0.9.0 / arch 形如 amd64 / arm64 / bin 形如 desktop
# 后端自动选择：
#   Linux runner：wixl（msitools，apt install msitools）
#   Windows runner：candle + light（WiX v3，choco install wixtoolset）
# 产物：dist/lanchat-<bin>-<version>-windows-<arch>.msi
set -euo pipefail

VERSION="${1:?usage: pkg-windows-msi.sh <version> <arch> <bin>}"
ARCH="${2:?}"
BIN="${3:?}"

case "$BIN" in
  desktop) APPNAME="LAN Chat";      EXE="lanchat-desktop.exe" ;;
  hub)     APPNAME="LAN Chat Hub";  EXE="hub.exe" ;;
  tui)     APPNAME="LAN Chat TUI";  EXE="tui.exe" ;;
  web)     APPNAME="LAN Chat Web";  EXE="web.exe" ;;
  *) echo "unknown bin: $BIN" >&2; exit 1 ;;
esac

NAME="lanchat-${BIN}-${VERSION}-windows-${ARCH}.msi"
ROOT="$(pwd)"
RAW="${ROOT}/dist/raw"
ABS_EXE="${RAW}/${EXE}"
ABS_OUT="${ROOT}/dist/${NAME}"
WXS="${ROOT}/.github/installer/lanchat.wxs"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

test -f "$ABS_EXE" || { echo "missing exe: $ABS_EXE" >&2; exit 1; }

if command -v wixl >/dev/null 2>&1; then
  # Linux / msitools：单命令直接出 msi
  wixl \
    -DVersion="${VERSION}" \
    -DProductName="${APPNAME}" \
    -DExePath="${ABS_EXE}" \
    -o "$ABS_OUT" \
    "$WXS"
elif command -v candle >/dev/null 2>&1; then
  # Windows / WiX v3：candle 编译 wxs -> wixobj，light 链接 -> msi
  candle -nologo \
    -dVersion="${VERSION}" \
    -dProductName="${APPNAME}" \
    -dExePath="${ABS_EXE}" \
    -out "$WORK/lanchat.wixobj" \
    "$WXS"
  light -nologo \
    -out "$ABS_OUT" \
    "$WORK/lanchat.wixobj"
else
  # MSI 是锦上添花，后端缺失不阻塞 release（只产 NSIS exe）。
  echo "WARNING: no MSI backend (wixl/candle), skipping MSI; NSIS 仍可用" >&2
  exit 0
fi

echo "built dist/${NAME}"
