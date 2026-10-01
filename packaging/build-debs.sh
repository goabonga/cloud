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

# One-line summaries and extended descriptions, keyed by package. lintian
# treats an empty extended description as an error, and a package with no
# prose is unreviewable anyway - `apt show` is where an operator finds out
# what a daemon on their host actually does.
declare -A SUMMARY=(
  [infra]="command-line client for the infra control plane"
  [infra-api]="infra control-plane API server"
  [infra-agent]="infra per-host agent for kernel reconciliation"
  [infra-controller-manager]="infra cluster reconcilers and scheduler"
  [terraform-provider-infra]="Terraform provider for infra resources"
  [infra-exporter]="Prometheus exporter for infra cluster state"
  [infra-idp]="infra identity provider and JWT issuer"
  [infra-container-init]="PID 1 for infra-managed OCI containers"
)
declare -A DESCRIPTION=(
  [infra]="Declarative CLI for applying and inspecting infra resources: VPCs,
ACL policies, secrets and SSL certificate authorities."
  [infra-api]="Serves the declarative resource API backed either by a local
state directory or, for high availability, by PostgreSQL."
  [infra-agent]="Reconciles the desired cluster state onto Linux primitives on
each host: network namespaces, nftables rules, cgroups v2, dm-crypt volumes
and OCI containers. Requires root and CAP_NET_ADMIN."
  [infra-controller-manager]="Runs the resource reconcilers and the binpack
and spread schedulers under leader election."
  [terraform-provider-infra]="Manages infra resources from Terraform. Install
under the Terraform plugin directory rather than invoking it directly."
  [infra-exporter]="Exposes cluster resource counts and reconciliation state
as Prometheus metrics on /metrics."
  [infra-idp]="Issues short-lived ES256 JWTs and publishes the matching JWKS
for the control-plane API to verify."
  [infra-container-init]="Minimal init process for containers the agent
starts: reaps zombies and forwards signals to the workload."
)

# Further units a package ships next to its service, from deploy/systemd.
# infra-agent starts infra-netns@ instances itself, one per namespace it holds,
# and an infra-lb@ instance in each that has listeners.
declare -A EXTRA_UNITS=(
  [infra-agent]="infra-netns@.service infra-lb@.service"
)

# Further binaries a package ships, as "cmd-dir:binary". infra-agent runs
# infra-lb, the load balancers' data plane.
declare -A EXTRA_BINARIES=(
  [infra-agent]="lb:infra-lb"
)

# component -> "cmd-dir:binary:service-unit" (empty service = not a daemon)
declare -A COMPONENTS=(
  [infra]="cli:infra:"
  [infra-api]="api:infra-api:infra-api"
  [infra-agent]="agent:infra-agent:infra-agent"
  [infra-controller-manager]="controller-manager:infra-controller-manager:infra-controller-manager"
  [terraform-provider-infra]="provider:terraform-provider-infra:"
  [infra-exporter]="exporter:infra-exporter:infra-exporter"
  [infra-idp]="idp:infra-idp:infra-idp"
  [infra-container-init]="container-init:infra-container-init:"
)

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
  # SOURCE_DATE_EPOCH keeps the stanza date reproducible when the caller sets
  # it; gzip -n drops the mtime for the same reason.
  local stamp
  stamp=$(date -R ${SOURCE_DATE_EPOCH:+--date="@$SOURCE_DATE_EPOCH"})
  cat >"$stage/usr/share/doc/$pkg/changelog" <<EOF
$pkg ($VERSION) unstable; urgency=medium

  * Release $VERSION. Full history: cmd/$dir/CHANGELOG.md.

 -- $MAINTAINER  $stamp
EOF
  gzip -9n "$stage/usr/share/doc/$pkg/changelog"
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
