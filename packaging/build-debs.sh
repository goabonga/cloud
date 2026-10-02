#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Build Debian packages for the Go components.
#
# Usage:
#   packaging/build-debs.sh [VERSION] [component...]
#
# VERSION defaults to $VERSION or 0.0.0; ARCH defaults to $ARCH or amd64.
# With no components, every component is built. Output lands in dist/.

set -euo pipefail

VERSION="${1:-${VERSION:-0.0.0}}"
if [ "$#" -ge 1 ]; then shift; fi
ARCH="${ARCH:-amd64}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
MAINTAINER="Chris <goabonga@pm.me>"

# SUMMARY, DESCRIPTION, EXTRA_UNITS, EXTRA_BINARIES, COMPONENTS: shared with
# build-archlinux.sh, so the two packaging formats' descriptions can't drift.
source "$ROOT/packaging/components.sh"

build_deb() {
  local pkg="$1"
  if [ -z "${COMPONENTS[$pkg]:-}" ]; then
    echo "unknown component: $pkg" >&2
    return 1
  fi
  local dir bin svc
  IFS=: read -r dir bin svc <<<"${COMPONENTS[$pkg]}"

  local stage
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' RETURN

  # infra-www embeds the SPA via go:embed, so it is stale the moment the
  # frontend changes underneath it: stage a fresh build before compiling.
  if [ "$pkg" = "infra-www" ]; then
    ( cd "$ROOT/www" && npm ci && npm run build )
    find "$ROOT/cmd/www/dist" -mindepth 1 ! -name .gitkeep -delete
    cp -r "$ROOT/www/dist/." "$ROOT/cmd/www/dist/"
  fi

  # /usr/bin, not /usr/local/bin: the latter is reserved for what the local
  # administrator installs by hand, and lintian rejects a package that writes
  # there. Hand-installed release binaries in the docs still use
  # /usr/local/bin, which is correct for that case.
  install -d "$stage/DEBIAN" "$stage/usr/bin"
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
    go build -ldflags='-s -w' -o "$stage/usr/bin/$bin" "$ROOT/cmd/$dir/"
  chmod 0755 "$stage/usr/bin/$bin"
  local extra_bin extra_dir extra_name
  for extra_bin in ${EXTRA_BINARIES[$pkg]:-}; do
    IFS=: read -r extra_dir extra_name <<<"$extra_bin"
    CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" \
      go build -ldflags='-s -w' -o "$stage/usr/bin/$extra_name" "$ROOT/cmd/$extra_dir/"
    chmod 0755 "$stage/usr/bin/$extra_name"
  done

  # Only the daemons run a maintainer script, and only those need adduser.
  local depends=""
  if [ -n "$svc" ]; then
    depends="Depends: adduser, systemd"
  fi

  # The extended description is the continuation lines: each is prefixed with
  # a single space, and an empty line becomes " .".
  local extended
  extended=$(printf '%s\n' "${DESCRIPTION[$pkg]}" \
    | sed -e 's/^$/./' -e 's/^/ /')

  cat >"$stage/DEBIAN/control" <<EOF
Package: $pkg
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Maintainer: $MAINTAINER
${depends}
Homepage: https://github.com/goabonga/infrastructure
Description: ${SUMMARY[$pkg]}
$extended
EOF
  # Drop the blank line the empty $depends leaves behind: a stray empty line
  # in a control file terminates the stanza, so everything after it is
  # silently ignored.
  sed -i '/^$/d' "$stage/DEBIAN/control"

  # DEP-5 copyright and a Debian changelog. Both are lintian errors when
  # absent, and both are where `dpkg -L` users look for licensing and history.
  install -d "$stage/usr/share/doc/$pkg"
  cat >"$stage/usr/share/doc/$pkg/copyright" <<EOF
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: infrastructure
Upstream-Contact: $MAINTAINER
Source: https://github.com/goabonga/infrastructure

Files: *
Copyright: 2026 Chris <goabonga@pm.me>
License: MIT
 Permission is hereby granted, free of charge, to any person obtaining a copy
 of this software and associated documentation files (the "Software"), to deal
 in the Software without restriction, including without limitation the rights
 to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 copies of the Software, and to permit persons to whom the Software is
 furnished to do so, subject to the following conditions:
 .
 The above copyright notice and this permission notice shall be included in
 all copies or substantial portions of the Software.
 .
 THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
 THE SOFTWARE.
EOF
  # multicz's debian-changelog writer (multicz.toml) maintains the real file,
  # prepending a dch(1)-format stanza - grouped by the conventional commits
  # that drove the bump - every time `multicz bump` actually bumps this
  # component. bump-preview's write-only call never commits, so the file
  # only lands in the repo via a real `cut-release` run for THIS component -
  # and `deb` builds all nine every run regardless of which one, if any,
  # bumped. A component that has not bumped even once since this writer was
  # added therefore has no committed file yet to read, so fall back to a
  # minimal synthetic stanza in that case rather than failing the build -
  # the real file takes over for good the first time this component ships a
  # real release.
  local real_changelog="$ROOT/cmd/$dir/debian/changelog"
  if [ -f "$real_changelog" ]; then
    gzip -9n -c "$real_changelog" >"$stage/usr/share/doc/$pkg/changelog.gz"
  else
    local stamp
    stamp=$(date -R ${SOURCE_DATE_EPOCH:+--date="@$SOURCE_DATE_EPOCH"})
    cat <<EOF | gzip -9n >"$stage/usr/share/doc/$pkg/changelog.gz"
$pkg ($VERSION-1) unstable; urgency=medium

  * Release $VERSION. Full history: cmd/$dir/CHANGELOG.md.

 -- $MAINTAINER  $stamp
EOF
  fi
  # Explicit, because these were written under the builder's umask and a
  # group-writable file in a package is a lintian warning.
  chmod 0644 "$stage/usr/share/doc/$pkg/copyright" \
             "$stage/usr/share/doc/$pkg/changelog.gz"

  # A Go binary built with CGO_ENABLED=0 is static by construction - that is
  # what makes it deployable without a runtime dependency chain, and what
  # lintian flags. Override the tag rather than weaken the build.
  install -d "$stage/usr/share/lintian/overrides"
  cat >"$stage/usr/share/lintian/overrides/$pkg" <<EOF
# Built with CGO_ENABLED=0 on purpose: a static binary is the deployment unit
# this project ships, so there is no dynamic linkage to restore.
$pkg: statically-linked-binary [usr/bin/$bin]
EOF
  for extra_bin in ${EXTRA_BINARIES[$pkg]:-}; do
    echo "$pkg: statically-linked-binary [usr/bin/${extra_bin#*:}]" >>"$stage/usr/share/lintian/overrides/$pkg"
  done
  chmod 0644 "$stage/usr/share/lintian/overrides/$pkg"

  if [ -n "$svc" ]; then
    # /usr/lib, not /lib: on a usrmerge system the latter is a symlink, and
    # shipping a path that resolves through it is a lintian error.
    install -d "$stage/usr/lib/systemd/system"
    install -m 0644 "$ROOT/deploy/systemd/$svc.service" \
      "$stage/usr/lib/systemd/system/$svc.service"
    local extra
    for extra in ${EXTRA_UNITS[$pkg]:-}; do
      install -m 0644 "$ROOT/deploy/systemd/$extra" "$stage/usr/lib/systemd/system/$extra"
    done
    # Every unit references the `infra` system user or group, so each package
    # shipping one has to be able to create it - packages install in any
    # order and none can assume a sibling went first. adduser --system is
    # idempotent, so the second package through is a no-op.
    #
    # No home directory and no login shell: the account exists to own
    # /var/lib/infra and to be the User= of a daemon, nothing else.
    cat >"$stage/DEBIAN/postinst" <<EOF
#!/bin/sh
set -e
if ! getent group infra >/dev/null; then
  addgroup --system infra
fi
if ! getent passwd infra >/dev/null; then
  adduser --system --ingroup infra --no-create-home \\
    --home /var/lib/infra --shell /usr/sbin/nologin infra
fi
systemctl daemon-reload || true
EOF
    cat >"$stage/DEBIAN/prerm" <<EOF
#!/bin/sh
set -e
if [ "\$1" = remove ]; then
  systemctl disable --now $svc.service || true
$(for extra in ${EXTRA_UNITS[$pkg]:-}; do echo "  systemctl stop '${extra/@./@*.}' || true"; done)
fi
EOF
    # The `infra` user is deliberately NOT removed on purge: it owns
    # /var/lib/infra, and reaping the account would leave the cluster state
    # owned by a recycled UID.
    chmod 0755 "$stage/DEBIAN/postinst" "$stage/DEBIAN/prerm"
  fi

  install -d "$DIST"
  dpkg-deb --root-owner-group --build "$stage" "$DIST/${pkg}_${VERSION}_${ARCH}.deb"
}

targets=("$@")
if [ "${#targets[@]}" -eq 0 ]; then
  targets=("${!COMPONENTS[@]}")
fi
for pkg in "${targets[@]}"; do
  build_deb "$pkg"
done
echo "built: $(ls "$DIST"/*.deb 2>/dev/null | wc -l) package(s) in $DIST"
