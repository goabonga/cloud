#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Validate an installed infra Arch package.
#
# Run against a .pkg.tar.zst that has already been installed with pacman -U.
# Checks the package is well-formed and the binary it ships actually loads -
# namcap, run separately by validate-archlinux's caller, already covers
# packaging QA the way lintian does for the .deb.
#
# Deliberately does NOT start the service and probe its health port the way
# test/install/validate.sh does for the .deb: this runs inside a plain
# archlinux:base-devel container, whose PID 1 is not systemd, so there is no
# live instance to start one against. `systemd-analyze verify` below still
# statically checks the unit parses, and it is the exact same unit file
# deploy/systemd ships to the .deb - deb-validate already proves that file
# starts cleanly and scores within its security threshold, so re-proving it
# live here would test the packaging format, not the unit, a second time.
#
# Usage:
#   validate-archlinux.sh <package>

set -euo pipefail

PKG="${1:?usage: validate-archlinux.sh <package>}"

# package -> "binary:unit". Empty unit = no daemon.
declare -A EXPECT=(
  [infra]="infra:"
  [infra-api]="infra-api:infra-api"
  [infra-idp]="infra-idp:infra-idp"
  [infra-exporter]="infra-exporter:infra-exporter"
  [infra-controller-manager]="infra-controller-manager:infra-controller-manager"
  [infra-agent]="infra-agent:infra-agent"
  [infra-container-init]="infra-container-init:"
  [terraform-provider-infra]="terraform-provider-infra:"
  [infra-www]="infra-www:infra-www"
)

if [ -z "${EXPECT[$PKG]:-}" ]; then
  echo "validate-archlinux: unknown package $PKG" >&2
  exit 1
fi
IFS=: read -r BIN UNIT <<<"${EXPECT[$PKG]}"

fail() { echo "validate-archlinux($PKG): FAIL - $1" >&2; exit 1; }
ok() { echo "validate-archlinux($PKG): ok - $1"; }

# --- 1. the package is installed and its file list is real -----------------

pacman -Qi "$PKG" > /dev/null 2>&1 || fail "package is not installed"
ok "package installed"

while IFS= read -r path; do
  [ -e "/$path" ] || fail "pacman lists $path but it does not exist"
done < <(pacman -Qlq "$PKG" | sed 's#^/##')
ok "every path pacman lists exists"

# --- 2. policy files a package must carry ----------------------------------

[ -f "/usr/share/licenses/$PKG/LICENSE" ] || fail "no license file"
ok "license present"

# --- 3. the binary loads and runs ------------------------------------------

[ -x "/usr/bin/$BIN" ] || fail "/usr/bin/$BIN is missing or not executable"
# Exit status is deliberately not asserted - see test/install/validate.sh's
# identical reasoning for the .deb.
set +e
timeout 10s "/usr/bin/$BIN" --help > /dev/null 2>&1
rc=$?
set -e
case "$rc" in
  126 | 127) fail "/usr/bin/$BIN could not be executed (exit $rc)" ;;
  13[0-9] | 1[4-9][0-9]) fail "/usr/bin/$BIN died on a signal (exit $rc)" ;;
  *) ok "binary executes (exit $rc)" ;;
esac

# --- 4. the unit, if any, at least parses -----------------------------------

if [ -z "$UNIT" ]; then
  echo "validate-archlinux($PKG): no unit shipped; done"
  exit 0
fi

UNIT_PATH="/usr/lib/systemd/system/$UNIT.service"
[ -f "$UNIT_PATH" ] || fail "unit $UNIT_PATH not installed"
systemd-analyze verify "$UNIT_PATH" || fail "unit does not parse"
ok "unit parses"

[ -f "/usr/lib/sysusers.d/$PKG.conf" ] || fail "no sysusers.d entry for the infra service account"
ok "sysusers.d entry present"
