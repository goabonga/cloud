#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Publish one component's PKGBUILD + .SRCINFO to its AUR git repository.
#
# Usage:
#   push-aur.sh <component>
#
# Renders the PKGBUILD against the real GitHub release tarball for
# <component>-v<version> - which must already exist, since this runs after
# the release job has tagged it and publish-component has cut the Release -
# rather than the local file:// tarball build-archlinux.sh uses: what AUR
# hosts has to resolve for anyone building it fresh, not just this CI run.
#
# .SRCINFO is generated with the real `makepkg --printsrcinfo`, which only
# exists inside an Arch userland. This shells out to Docker for that one
# step rather than requiring the whole `publish` job to run in an Arch
# container, since it is the only thing here that needs it.
#
# Requires: a git identity (user.name/user.email) and GIT_SSH_COMMAND
# pointed at a key for the aur@aur.archlinux.org account, both set up by
# push-aur's composite action before this runs.

set -euo pipefail

COMPONENT="${1:?usage: push-aur.sh <component>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# COMPONENTS: shared with build-debs.sh/build-archlinux.sh.
# render_pkgbuild: shared with build-archlinux.sh, so the AUR PKGBUILD and
# the one in the downloadable .pkg.tar.zst never drift apart.
source "$ROOT/packaging/components.sh"
source "$ROOT/packaging/archlinux-pkgbuild.sh"

if [ -z "${COMPONENTS[$COMPONENT]:-}" ]; then
  echo "push-aur: unknown component $COMPONENT" >&2
  exit 1
fi
IFS=: read -r dir bin svc <<<"${COMPONENTS[$COMPONENT]}"

VERSION=$(multicz get "$COMPONENT")
tag="${COMPONENT}-v${VERSION}"
archive_url="https://github.com/goabonga/infrastructure/archive/refs/tags/${tag}.tar.gz"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

tarball="$work/src.tar.gz"
curl -fsSL -o "$tarball" "$archive_url"
sha256=$(sha256sum "$tarball" | cut -d' ' -f1)

render_pkgbuild "$COMPONENT" "$VERSION" "$dir" "$bin" "$svc" "$work/PKGBUILD" \
  "\"$COMPONENT-$VERSION.tar.gz::$archive_url\"" "$sha256"

# makepkg refuses root, same reason build-archlinux.sh needs a throwaway
# "builder" user - done inside the container here rather than on the host,
# which is Ubuntu and has no makepkg at all.
docker run --rm -v "$work":/pkg -w /pkg archlinux:base-devel bash -c '
  set -euo pipefail
  pacman -Sy --noconfirm --needed base-devel >/dev/null
  useradd -m builder
  chown -R builder:builder /pkg
  runuser -u builder -- makepkg --printsrcinfo > /pkg/.SRCINFO
'

# A clone of a pkgbase AUR has never seen succeeds with an explicit "empty
# repository" warning rather than failing - the AUR registers the package on
# first push, not on clone - so this needs no separate "does it exist yet"
# branch for a component's first release.
repo="$work/aur"
git clone "ssh://aur@aur.archlinux.org/${COMPONENT}.git" "$repo"
cp "$work/PKGBUILD" "$work/.SRCINFO" "$repo/"
cd "$repo"
git add PKGBUILD .SRCINFO
if git diff --cached --quiet; then
  echo "push-aur($COMPONENT): PKGBUILD/.SRCINFO unchanged, nothing to push"
  exit 0
fi
git commit -m "$tag"
git push origin HEAD:master
