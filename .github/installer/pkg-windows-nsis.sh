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
# EXE/OUTFILE 传绝对路径：NSIS 的 File/OutFile 相对脚本文件所在目录解析，
# 与 makensis 的 cwd 无关。
ROOT="$(pwd)"
RAW="${ROOT}/dist/raw"
ABS_EXE="${RAW}/${EXE}"
ABS_OUT="${ROOT}/dist/${NAME}"
SCRIPT="${ROOT}/.github/installer/lanchat.nsi"
# Windows runner（Git Bash）里 $(pwd) 是 /d/a/... 风格，makensis.exe 是
# 原生 Windows 程序不认 POSIX 路径；cygpath -m 转成 D:/a/...（正斜杠）。
# 不用 OSTYPE 判断：GitHub Actions 的 Git Bash 偶发不设置 OSTYPE（实测
# desktop(windows-latest) job 因此漏转、makensis 把 /d/... 当 /D 选项）。
# 直接探测 cygpath 是否可用——Git Bash 必有，Linux 必无，语义最稳。
if command -v cygpath >/dev/null 2>&1; then
  # cygpath -w 出反斜杠路径（D:\a\...）；NSIS 的 File/OutFile 对正斜杠
  # 盘符路径实测 no files found（2026-09-08 desktop windows job），
  # 反斜杠是 Windows 原生格式，makensis 解析最稳。
  ABS_EXE="$(cygpath -w "$ABS_EXE")"
  ABS_OUT="$(cygpath -w "$ABS_OUT")"
  SCRIPT="$(cygpath -w "$SCRIPT")"
fi
( cd "$RAW" && MSYS_NO_PATHCONV=1 "$MAKENSIS" \
    -DVERSION="${VERSION}" -DAPPNAME="${APPNAME}" -DEXE="${ABS_EXE}" \
    -DOUTFILE="${ABS_OUT}" "$SCRIPT" )
echo "built dist/${NAME}"
