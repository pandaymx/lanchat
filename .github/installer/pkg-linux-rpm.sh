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
cat > "$topdir/SPECS/lanchat.spec" <<EOF
Name: ${PACKAGE}
Version: ${VERSION}
Release: 1%{?dist}
Summary: LAN Chat ${BIN}
License: MIT
URL: https://github.com/pandaymx/lanchat
BuildArch: ${ARCH}
${REQUIRES:+Requires: ${REQUIRES}}

%description
LAN Chat ${BIN}. Chat over LAN without internet.

%prep

%build

%install
install -m 0755 %{_sourcedir}/${BIN} %{buildroot}/usr/bin/${BIN}
%if [ "$KIND" = "desktop" ]
install -d %{buildroot}%{_datadir}/applications
install -m 0644 %{_sourcedir}/lanchat.desktop %{buildroot}%{_datadir}/applications/
%endif

%files
/usr/bin/${BIN}
%if [ "$KIND" = "desktop" ]
%{_datadir}/applications/lanchat.desktop
%endif

%changelog
EOF
rpmbuild -bb --define "_topdir $topdir" --target "${ARCH}-linux" "$topdir/SPECS/lanchat.spec" >/dev/null
found=$(find "$topdir/RPMS" -name '*.rpm' | head -1)
[ -n "$found" ] || { echo "rpmbuild produced no rpm" >&2; exit 1; }
install -m 0644 "$found" "dist/$NAME"
echo "built dist/$NAME"
