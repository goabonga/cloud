#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Run govulncheck and fail on any vulnerability the code reaches, except those
# osv-scanner.toml accepts until a date not yet past. govulncheck has no
# allow-list of its own; this keeps one list for both scanners.
#
# Usage: scripts/govulncheck.sh [packages...]   (default ./...)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
today=$(date -u +%F)

# id <TAB> ignoreUntil, for each [[IgnoredVulns]] entry.
accepted=$(awk -F' = ' '
  /^\[\[IgnoredVulns\]\]/ { if (id != "") print id "\t" until; id = ""; until = "" }
  $1 == "id"          { gsub(/"/, "", $2); id = $2 }
  $1 == "ignoreUntil" { until = $2 }
  END                 { if (id != "") print id "\t" until }
' "$ROOT/osv-scanner.toml")

report=$(mktemp)
trap 'rm -f "$report"' EXIT
govulncheck -format json "${@:-./...}" > "$report"

# A finding whose trace starts at a function is one the code calls - what
# govulncheck's default output reports and fails on.
reached=$(jq -r 'select(.finding != null and .finding.trace[0].function != null) | .finding.osv' "$report" | sort -u)

status=0
for id in $reached; do
  until=$(printf '%s\n' "$accepted" | awk -F'\t' -v id="$id" '$1 == id { print $2 }')
  if [ -n "$until" ] && [[ "$today" < "$until" ]]; then
    echo "accepted until $until: $id (see osv-scanner.toml)"
    continue
  fi
  if [ -n "$until" ]; then
    echo "::error::$id was accepted until $until: review it again"
  else
    echo "::error::$id is reached by the code; see https://pkg.go.dev/vuln/$id"
  fi
  status=1
done
if [ "$status" -ne 0 ]; then
  govulncheck "${@:-./...}" || true
fi
exit "$status"
