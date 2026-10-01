#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Build Arch Linux packages for the Go components with makepkg.
#
# Usage:
#   packaging/build-archlinux.sh [VERSION] [component...]
#
# VERSION defaults to $VERSION or 0.0.0. With no components, every component
# is built. Output lands in dist/.
#
# Must run where makepkg/base-devel are already installed (the
# build-archlinux composite action runs this inside an archlinux:base-devel
# container) and as a NON-root user - makepkg refuses to run as root, same as
# it would refuse a real AUR build.
#
# The PKGBUILD this renders always points source= at a local tarball of this
# checkout (built fresh per component below), never at the GitHub release
# tarball: on a pull request there is no tag yet for the version bump-preview
# computed, only the working tree. push-aur.sh renders its own PKGBUILD
# against the real tag once one exists, for the copy actually published to
# the AUR.

set -euo pipefail

VERSION="${1:-${VERSION:-0.0.0}}"
if [ "$#" -ge 1 ]; then shift; fi
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"

# SUMMARY, DESCRIPTION, EXTRA_UNITS, EXTRA_BINARIES, COMPONENTS: shared with
# build-debs.sh. render_pkgbuild: shared with push-aur.sh.
source "$ROOT/packaging/components.sh"
source "$ROOT/packaging/archlinux-pkgbuild.sh"

# makepkg's own arch naming, not Go's GOARCH - this script always builds for
# the host architecture it runs on (the archlinux job's matrix picks the
# runner, same as `deb` building one amd64 artifact per leg).
case "$(uname -m)" in
  x86_64 | aarch64) PKG_ARCH="$(uname -m)" ;;
  *) echo "build-archlinux: unsupported host arch $(uname -m)" >&2; exit 1 ;;
esac

build_pkg() {
  local pkg="$1"
  if [ -z "${COMPONENTS[$pkg]:-}" ]; then
    echo "unknown component: $pkg" >&2
    return 1
  fi
  local dir bin svc
  IFS=: read -r dir bin svc <<<"${COMPONENTS[$pkg]}"

  # infra-www embeds the SPA via go:embed, so it is stale the moment the
  # frontend changes underneath it: stage a fresh build before packaging,
  # same as build-debs.sh.
  if [ "$pkg" = "infra-www" ]; then
    ( cd "$ROOT/www" && npm ci && npm run build )
    find "$ROOT/cmd/www/dist" -mindepth 1 ! -name .gitkeep -delete
    cp -r "$ROOT/www/dist/." "$ROOT/cmd/www/dist/"
  fi

  local work tarball sha256
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' RETURN

  # A tarball of the current working tree, not the real release asset: see
  # the file header for why. Deliberately not `git archive HEAD` - that
  # would miss bump-preview's uncommitted version.go edit, which is exactly
  # the version string this build needs to embed. --transform wraps it in a
  # single top-level directory, matching the shape of a real GitHub release
  # tarball, so package()/build() below can `cd` into it the same way either
  # source produces.
  tarball="$work/src.tar.gz"
  tar --exclude-vcs --exclude='./dist' --exclude='./www/node_modules' --exclude='./www/dist' \
    --transform="s,^\.,${pkg}-${VERSION}," \
    -czf "$tarball" -C "$ROOT" .
  sha256=$(sha256sum "$tarball" | cut -d' ' -f1)

  render_pkgbuild "$pkg" "$VERSION" "$dir" "$bin" "$svc" "$work/PKGBUILD" \
    "\"$pkg-$VERSION.tar.gz::file://$tarball\"" "$sha256"

  install -d "$DIST"
  ( cd "$work" && makepkg --noconfirm --skippgpcheck )
  cp "$work/${pkg}-${VERSION}-1-${PKG_ARCH}.pkg.tar.zst" "$DIST/"
}

targets=("$@")
if [ "${#targets[@]}" -eq 0 ]; then
  targets=("${!COMPONENTS[@]}")
fi
for pkg in "${targets[@]}"; do
  build_pkg "$pkg"
done
echo "built: $(ls "$DIST"/*.pkg.tar.zst 2>/dev/null | wc -l) package(s) in $DIST"
