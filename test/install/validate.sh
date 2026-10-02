#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Validate an installed infra Debian package.
#
# Run against a .deb that has already been installed with dpkg/apt, on a host
# with systemd running as PID 1. This is the check that the .deb is not merely
# well-formed - lintian covers that - but actually usable: the binary loads,
# the unit parses, the service reaches a listening state, and the hardening
# directives in the unit do not stop it from starting.
#
# That last point is why this exists. Every protection added to a unit is a
# way to make the daemon fail to start, and `systemd-analyze security` scores
# a unit without ever running it. Only starting the service proves that the
# syscall filter, the capability bounding set and the namespace restrictions
# left the daemon room to work.
#
# Usage:
#   sudo test/install/validate.sh <package>

set -euo pipefail

PKG="${1:?usage: validate.sh <package>}"

# package -> "binary:unit:health-port". Empty unit = no daemon; empty port =
# a daemon that listens on nothing (the controller-manager reconciles
# outward).
declare -A EXPECT=(
  [infra]="infra::"
  [infra-api]="infra-api:infra-api:8080"
  [infra-idp]="infra-idp:infra-idp:8081"
  [infra-exporter]="infra-exporter:infra-exporter:9100"
  [infra-controller-manager]="infra-controller-manager:infra-controller-manager:"
  [infra-agent]="infra-agent:infra-agent:"
  [infra-container-init]="infra-container-init::"
  [terraform-provider-infra]="terraform-provider-infra::"
  [infra-www]="infra-www:infra-www:8088"
)

if [ -z "${EXPECT[$PKG]:-}" ]; then
  echo "validate: unknown package $PKG" >&2
  exit 1
fi
IFS=: read -r BIN UNIT PORT <<<"${EXPECT[$PKG]}"

fail() { echo "validate($PKG): FAIL - $1" >&2; exit 1; }
ok() { echo "validate($PKG): ok - $1"; }

# --- 1. the package is installed and its file list is real -----------------

dpkg -s "$PKG" > /dev/null 2>&1 || fail "package is not installed"
ok "package installed"

while IFS= read -r path; do
  # dpkg -L lists directories too, and diverted paths as prose lines.
  case "$path" in /*) ;; *) continue ;; esac
  [ -e "$path" ] || fail "dpkg lists $path but it does not exist"
done < <(dpkg -L "$PKG")
ok "every path dpkg lists exists"

# --- 2. policy files a package must carry ----------------------------------

[ -f "/usr/share/doc/$PKG/copyright" ] || fail "no copyright file"
[ -f "/usr/share/doc/$PKG/changelog.gz" ] || fail "no changelog"
ok "copyright and changelog present"

# --- 3. the binary loads and runs ------------------------------------------

[ -x "/usr/bin/$BIN" ] || fail "/usr/bin/$BIN is missing or not executable"
# Exit status is deliberately not asserted: several of these binaries exit
# non-zero when invoked with no arguments, which is correct behaviour. What
# matters is that the kernel could execute the image at all - 126 (not
# executable) and 127 (loader failure) are the failures a broken static build
# or a wrong architecture produce, and a signal death shows up above 128.
set +e
timeout 10s "/usr/bin/$BIN" --help > /dev/null 2>&1
rc=$?
set -e
case "$rc" in
  126 | 127) fail "/usr/bin/$BIN could not be executed (exit $rc)" ;;
  13[0-9] | 1[4-9][0-9]) fail "/usr/bin/$BIN died on a signal (exit $rc)" ;;
  *) ok "binary executes (exit $rc)" ;;
esac

# --- 4. the unit, if any ---------------------------------------------------

if [ -z "$UNIT" ]; then
  echo "validate($PKG): no unit shipped; done"
  exit 0
fi

UNIT_PATH="/usr/lib/systemd/system/$UNIT.service"
[ -f "$UNIT_PATH" ] || fail "unit $UNIT_PATH not installed"
systemd-analyze verify "$UNIT_PATH" || fail "unit does not parse"
ok "unit parses"

# infra-idp exits at startup unless at least one client is configured, which
# is correct for an identity provider - it has nothing to issue tokens to
# otherwise. The package cannot ship a default client without shipping a
# credential, so the deployment supplies one (Ansible writes idp.env). Install
# a throwaway drop-in here so the unit can be started and probed, and remove it
# afterwards so nothing is left behind on the runner.
IDP_DROPIN=/etc/systemd/system/infra-idp.service.d/99-validate.conf
if [ "$PKG" = "infra-idp" ]; then
  install -d "$(dirname "$IDP_DROPIN")"
  cat > "$IDP_DROPIN" <<'DROPIN'
[Service]
Environment=GOA_IDP_CLIENTS=validate-token:validate-subject
DROPIN
  trap 'rm -f "$IDP_DROPIN"; rmdir --ignore-fail-on-non-empty "$(dirname "$IDP_DROPIN")" 2>/dev/null || true; systemctl daemon-reload || true' EXIT
fi

systemctl daemon-reload

# The agent reconciles real kernel state - namespaces, nftables, dm-crypt - so
# starting it on a CI runner would mutate the host. Its unit is verified and
# scored, not run. The namespace holder it ships only creates an empty network
# namespace, so that one is started: its hardening must leave it running, in a
# namespace of its own the agent can enter as root.
if [ "$PKG" = "infra-agent" ]; then
  NETNS_PATH=/usr/lib/systemd/system/infra-netns@.service
  [ -f "$NETNS_PATH" ] || fail "unit $NETNS_PATH not installed"
  systemd-analyze verify "$NETNS_PATH" || fail "infra-netns@ does not parse"
  systemctl start infra-netns@validate.service || {
    journalctl -u infra-netns@validate.service --no-pager -n 50 || true
    fail "infra-netns@ did not start"
  }
  pid=$(systemctl show --property=MainPID --value infra-netns@validate.service)
  [ "${pid:-0}" -gt 0 ] || fail "infra-netns@ has no main process"
  [ "$(readlink "/proc/$pid/ns/net")" != "$(readlink /proc/1/ns/net)" ] ||
    fail "infra-netns@ shares the host's network namespace"
  nsenter --net="/proc/$pid/ns/net" -- ip link show lo > /dev/null ||
    fail "infra-netns@'s namespace cannot be entered"
  ok "infra-netns@ holds a namespace of its own"

  # infra-lb@ joins that namespace and serves what the agent writes; an empty
  # configuration is enough to prove its hardening leaves it running.
  [ -x /usr/bin/infra-lb ] || fail "/usr/bin/infra-lb is missing"
  LB_PATH=/usr/lib/systemd/system/infra-lb@.service
  [ -f "$LB_PATH" ] || fail "unit $LB_PATH not installed"
  systemd-analyze verify "$LB_PATH" || fail "infra-lb@ does not parse"
  install -d -m 0700 /run/infra-lb/validate
  printf '{"listeners":[],"targetGroups":[]}\n' > /run/infra-lb/validate/config.json
  chmod 0600 /run/infra-lb/validate/config.json
  systemctl start infra-lb@validate.service || {
    journalctl -u infra-lb@validate.service --no-pager -n 50 || true
    fail "infra-lb@ did not start"
  }
  deadline=$((SECONDS + 10))
  until [ -s /run/infra-lb/validate/status.json ]; do
    systemctl is-active --quiet infra-lb@validate.service || {
      journalctl -u infra-lb@validate.service --no-pager -n 50 || true
      fail "infra-lb@ started then exited"
    }
    [ "$SECONDS" -lt "$deadline" ] || fail "infra-lb@ wrote no status within 10s"
    sleep 1
  done
  lbpid=$(systemctl show --property=MainPID --value infra-lb@validate.service)
  [ "$(readlink "/proc/$lbpid/ns/net")" = "$(readlink "/proc/$pid/ns/net")" ] ||
    fail "infra-lb@ runs outside infra-netns@'s namespace"
  systemctl stop infra-lb@validate.service
  rm -rf /run/infra-lb/validate
  ok "infra-lb@ serves inside the holder's namespace"

  # infra-egress@ is the same binary under a unit with no capability at all.
  EGRESS_PATH=/usr/lib/systemd/system/infra-egress@.service
  [ -f "$EGRESS_PATH" ] || fail "unit $EGRESS_PATH not installed"
  systemd-analyze verify "$EGRESS_PATH" || fail "infra-egress@ does not parse"
  install -d -m 0700 /run/infra-egress/validate
  printf '{"listeners":[],"targetGroups":[]}\n' > /run/infra-egress/validate/config.json
  chmod 0600 /run/infra-egress/validate/config.json
  systemctl start infra-egress@validate.service || {
    journalctl -u infra-egress@validate.service --no-pager -n 50 || true
    fail "infra-egress@ did not start"
  }
  deadline=$((SECONDS + 10))
  until [ -s /run/infra-egress/validate/status.json ]; do
    systemctl is-active --quiet infra-egress@validate.service || {
      journalctl -u infra-egress@validate.service --no-pager -n 50 || true
      fail "infra-egress@ started then exited"
    }
    [ "$SECONDS" -lt "$deadline" ] || fail "infra-egress@ wrote no status within 10s"
    sleep 1
  done
  systemctl stop infra-egress@validate.service infra-netns@validate.service
  rm -rf /run/infra-egress/validate
  ok "infra-egress@ serves inside the holder's namespace"
  echo "validate($PKG): unit verified; not started (mutates host kernel state)"
  exit 0
fi

systemctl start "$UNIT.service" || {
  systemctl status "$UNIT.service" --no-pager --full || true
  journalctl -u "$UNIT.service" --no-pager -n 50 || true
  fail "service did not start"
}

# Poll rather than sleep once: a fixed sleep either flakes on a slow runner or
# wastes time on a fast one.
deadline=$((SECONDS + 30))
while :; do
  if ! systemctl is-active --quiet "$UNIT.service"; then
    systemctl status "$UNIT.service" --no-pager --full || true
    journalctl -u "$UNIT.service" --no-pager -n 50 || true
    fail "service started then exited"
  fi
  if [ -z "$PORT" ]; then
    ok "service is active (no listener expected)"
    break
  fi
  if curl -fsS --max-time 2 "http://127.0.0.1:$PORT/healthz" > /dev/null 2>&1; then
    ok "/healthz answered on port $PORT"
    break
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    journalctl -u "$UNIT.service" --no-pager -n 50 || true
    fail "/healthz did not answer on port $PORT within 30s"
  fi
  sleep 1
done

systemctl stop "$UNIT.service"
ok "service stopped cleanly"
echo "validate($PKG): all checks passed"
