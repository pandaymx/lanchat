#!/usr/bin/env bash
# LAN Chat Linux .rpm 打包（RedHat / Fedora / openSUSE 系，M11.1）
# 用法（cwd = repo root）：pkg-linux-rpm.sh <version> <arch> <bin> [kind]
set -euo pipefail
VERSION="${1:?usage: pkg-linux-rpm.sh <version> <arch> <bin> [kind]}"
ARCH="${2:?}"
BIN="${3:?}"
KIND="${4:-desktop}"
[ "$BIN" = "desktop" ] && KIND="desktop" || KIND="${4:-cli}"
NAME="lanchat-${BIN}-${VERSION}-linux-${ARCH}.rpm"
PACKAGE="lanchat-${BIN}"
# rpm 架构名与 Go 不同：amd64 -> x86_64，arm64 -> aarch64
case "$ARCH" in
  amd64) RPMARCH="x86_64" ;;
  arm64) RPMARCH="aarch64" ;;
  *)     echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac
topdir="$(pwd)/dist/rpm-root"
rm -rf "$topdir"
mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
install -m 0755 "dist/raw/${BIN}" "$topdir/SOURCES/${BIN}"
if [ "$KIND" = "desktop" ]; then
  REQUIRES="gtk4, webkitgtk6.0, libsoup3"
  cat > "$topdir/SOURCES/lanchat.desktop" <<'DEOF'
[Desktop Entry]
Type=Application
Name=LAN Chat
Comment=Chat over LAN
Exec=lanchat-desktop
Terminal=false
Categories=Network;InstantMessaging;
DEOF
else
  REQUIRES=""
fi
# spec 由脚本按 KIND 直接生成（rpm %if 不支持 shell 条件，不能内嵌判断）
{
  echo "Name: ${PACKAGE}"
  echo "Version: ${VERSION}"
  echo "Release: 1%{?dist}"
  echo "Summary: LAN Chat ${BIN}"
  echo "License: MIT"
  echo "URL: https://github.com/pandaymx/lanchat"
  echo "BuildArch: ${RPMARCH}"
  [ -z "$REQUIRES" ] || echo "Requires: ${REQUIRES}"
  echo ""
  echo "%description"
  echo "LAN Chat ${BIN}. Chat over LAN without internet."
  echo ""
  echo "%prep"
  echo ""
  echo "%build"
  echo ""
  echo "%install"
  echo "install -m 0755 %{_sourcedir}/${BIN} %{buildroot}/usr/bin/${BIN}"
  if [ "$KIND" = "desktop" ]; then
    echo "install -d %{buildroot}%{_datadir}/applications"
    echo "install -m 0644 %{_sourcedir}/lanchat.desktop %{buildroot}%{_datadir}/applications/"
  fi
  echo ""
  echo "%files"
  echo "/usr/bin/${BIN}"
  if [ "$KIND" = "desktop" ]; then
    echo "%{_datadir}/applications/lanchat.desktop"
  fi
  echo ""
  echo "%changelog"
} > "$topdir/SPECS/lanchat.spec"
rpmbuild -bb --define "_topdir $topdir" --target "${RPMARCH}-linux" "$topdir/SPECS/lanchat.spec" >/dev/null
found=$(find "$topdir/RPMS" -name '*.rpm' | head -1)
[ -n "$found" ] || { echo "rpmbuild produced no rpm" >&2; exit 1; }
install -m 0644 "$found" "dist/$NAME"
echo "built dist/$NAME"
