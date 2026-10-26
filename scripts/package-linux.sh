#!/usr/bin/env bash
# package-linux.sh — 打包 Linux 发行版安装包：
#   1. LANChat-<ver>-linux-x64.tar.gz   （免安装便携压缩包）
#   2. LANChat-<ver>-linux-amd64.deb    （Ubuntu / Debian 安装包）
#   3. LANChat-<ver>-linux-x86_64.rpm   （Red Hat / RHEL / Fedora / Rocky / AlmaLinux 安装包）
#
# 用法：
#   VERSION=1.3.0 UI_BIN=ui-linux/target/release/lanchat-ui DAEMON_BIN=dist/stage/lanchat-daemon OUT_DIR=dist ./scripts/package-linux.sh
set -euo pipefail

VERSION="${VERSION:?需要 VERSION 环境变量（如 1.3.0）}"
VERSION="${VERSION#v}"
UI_BIN="${UI_BIN:-ui-linux/target/release/lanchat-ui}"
DAEMON_BIN="${DAEMON_BIN:-dist/stage/lanchat-daemon}"
OUT_DIR="${OUT_DIR:-dist}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DESKTOP_FILE="${REPO_ROOT}/ui-linux/packaging/dev.lanchat.LANChat.desktop"
ICON_FILE="${REPO_ROOT}/ui-linux/packaging/lanchat.svg"

if [[ ! -f "$UI_BIN" ]]; then
  echo "错误：未找到 UI 二进制文件: $UI_BIN" >&2
  exit 1
fi
if [[ ! -f "$DAEMON_BIN" ]]; then
  echo "错误：未找到 Daemon 二进制文件: $DAEMON_BIN" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"
OUT_DIR_ABS="$(cd "$OUT_DIR" && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# ─── 1. 便携式 tar.gz ──────────────────────────────────────────────
STAGE_TAR="${WORK_DIR}/tar"
mkdir -p "$STAGE_TAR"
install -m 0755 "$UI_BIN" "${STAGE_TAR}/lanchat-ui"
install -m 0755 "$DAEMON_BIN" "${STAGE_TAR}/lanchat-daemon"
tar -C "$STAGE_TAR" -czf "${OUT_DIR_ABS}/LANChat-${VERSION}-linux-x64.tar.gz" .

# ─── 2. Ubuntu / Debian (.deb) ────────────────────────────────────
if command -v dpkg-deb >/dev/null 2>&1; then
  DEB_ROOT="${WORK_DIR}/deb/lanchat_${VERSION}_amd64"
  mkdir -p "${DEB_ROOT}/DEBIAN"
  mkdir -p "${DEB_ROOT}/usr/bin"
  mkdir -p "${DEB_ROOT}/usr/share/applications"
  mkdir -p "${DEB_ROOT}/usr/share/icons/hicolor/scalable/apps"

  install -m 0755 "$UI_BIN" "${DEB_ROOT}/usr/bin/lanchat-ui"
  install -m 0755 "$DAEMON_BIN" "${DEB_ROOT}/usr/bin/lanchat-daemon"
  install -m 0644 "$DESKTOP_FILE" "${DEB_ROOT}/usr/share/applications/dev.lanchat.LANChat.desktop"
  install -m 0644 "$ICON_FILE" "${DEB_ROOT}/usr/share/icons/hicolor/scalable/apps/lanchat.svg"

  INSTALLED_SIZE="$(du -sk "${DEB_ROOT}/usr" | awk '{print $1}')"

  cat > "${DEB_ROOT}/DEBIAN/control" <<EOF
Package: lanchat
Version: ${VERSION}
Section: net
Priority: optional
Architecture: amd64
Depends: libgtk-4-1, libadwaita-1-0, ca-certificates
Installed-Size: ${INSTALLED_SIZE}
Maintainer: LANChat Contributors <noreply@lanchat.dev>
Homepage: https://github.com/pandaymx/lanchat
Description: LAN P2P Chat and File Transfer Client (GTK4 + libadwaita)
 Zero-configuration LAN P2P chat and high-speed file transfer client
 built with GTK4, libadwaita, and a Go P2P daemon.
EOF
  chmod 0755 "${DEB_ROOT}/DEBIAN"
  chmod 0644 "${DEB_ROOT}/DEBIAN/control"
  cat > "${DEB_ROOT}/DEBIAN/postinst" <<'POSTINST'
#!/bin/sh
set -e
pkill -9 -x lanchat-daemon >/dev/null 2>&1 || true
exit 0
POSTINST
  chmod 0755 "${DEB_ROOT}/DEBIAN/postinst"
  find "$DEB_ROOT" -type d -exec chmod 0755 {} +

  dpkg-deb --build --root-owner-group "$DEB_ROOT" "${OUT_DIR_ABS}/LANChat-${VERSION}-linux-amd64.deb"
else
  echo "警告：未检测到 dpkg-deb，跳过 .deb 构建" >&2
fi

# ─── 3. Red Hat / RHEL / Fedora / Rocky / AlmaLinux (.rpm) ────────
if command -v rpmbuild >/dev/null 2>&1; then
  RPM_TOP="${WORK_DIR}/rpmbuild"
  mkdir -p "${RPM_TOP}"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}

  # RPM 版本号不允许包含 '-'，将预发布标签中的 '-' 转为 '~'
  RPM_VERSION="${VERSION//-/\~}"
  UI_BIN_ABS="$(cd "$(dirname "$UI_BIN")" && pwd)/$(basename "$UI_BIN")"
  DAEMON_BIN_ABS="$(cd "$(dirname "$DAEMON_BIN")" && pwd)/$(basename "$DAEMON_BIN")"

  cat > "${RPM_TOP}/SPECS/lanchat.spec" <<EOF
%global debug_package %{nil}
%define _build_id_links none
%define __os_install_post %{nil}

Name:           lanchat
Version:        ${RPM_VERSION}
Release:        1
Summary:        LAN P2P Chat and File Transfer Client (GTK4 + libadwaita)
License:        MIT
URL:            https://github.com/pandaymx/lanchat
BuildArch:      x86_64
AutoReqProv:    no
Requires:       gtk4, libadwaita, ca-certificates

%description
Zero-configuration LAN P2P chat and high-speed file transfer client
built with GTK4, libadwaita, and a Go P2P daemon.

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/scalable/apps
install -m 0755 "${UI_BIN_ABS}" %{buildroot}/usr/bin/lanchat-ui
install -m 0755 "${DAEMON_BIN_ABS}" %{buildroot}/usr/bin/lanchat-daemon
install -m 0644 "${DESKTOP_FILE}" %{buildroot}/usr/share/applications/dev.lanchat.LANChat.desktop
install -m 0644 "${ICON_FILE}" %{buildroot}/usr/share/icons/hicolor/scalable/apps/lanchat.svg

%files
%attr(0755, root, root) /usr/bin/lanchat-ui
%attr(0755, root, root) /usr/bin/lanchat-daemon
%attr(0644, root, root) /usr/share/applications/dev.lanchat.LANChat.desktop
%attr(0644, root, root) /usr/share/icons/hicolor/scalable/apps/lanchat.svg

%post
pkill -9 -x lanchat-daemon >/dev/null 2>&1 || true
exit 0
EOF

  rpmbuild -bb \
    --define "_topdir ${RPM_TOP}" \
    --target x86_64 \
    "${RPM_TOP}/SPECS/lanchat.spec"

  RPM_BUILT="$(find "${RPM_TOP}/RPMS" -name "*.rpm" | head -n 1)"
  if [[ -z "$RPM_BUILT" ]]; then
    echo "错误：rpmbuild 未生成 .rpm 文件" >&2
    exit 1
  fi
  cp "$RPM_BUILT" "${OUT_DIR_ABS}/LANChat-${VERSION}-linux-x86_64.rpm"
else
  echo "警告：未检测到 rpmbuild，跳过 .rpm 构建" >&2
fi

ls -lh "${OUT_DIR_ABS}"/LANChat-"${VERSION}"-linux-*
