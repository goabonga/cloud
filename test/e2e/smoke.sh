#!/usr/bin/env bash

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# End-to-end smoke suite against a running control plane.
#
# Expects the four listening components reachable on localhost - the CI
# `stack-up` action builds them from source and starts them as plain
# processes. Nothing here is specific to that: point the env vars at any
# deployment and it works.
#
# What this covers that the unit and integration suites cannot: the resource
# round-trip crosses a real HTTP boundary, against PostgreSQL rather than the
# file store, with the dashboard proxying to the API over HTTP. Both are ways
# for a change that passes `go test` to be broken in the deployment that
# ships.
#
#   API=http://127.0.0.1:8080 test/e2e/smoke.sh

set -euo pipefail

API="${API:-http://127.0.0.1:8080}"
IDP="${IDP:-http://127.0.0.1:8081}"
EXPORTER="${EXPORTER:-http://127.0.0.1:9100}"
WWW="${WWW:-http://127.0.0.1:8088}"

UID_UNDER_TEST="vpc-e2e-$$"
failures=0

fail() { echo "  FAIL: $1" >&2; failures=$((failures + 1)); }
pass() { echo "  ok: $1"; }
step() { echo; echo "== $1"; }

req() {
  # req METHOD URL [BODY] -> prints "<status>\n<body>"
  local method="$1" url="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -sS -X "$method" "$url" \
      -H 'Content-Type: application/json' -d "$body" \
      -w '\n%{http_code}' --max-time 10
  else
    curl -sS -X "$method" "$url" -w '\n%{http_code}' --max-time 10
  fi
}

status_of() { printf '%s' "$1" | tail -n1; }
body_of() { printf '%s' "$1" | sed '$d'; }

step "health"
for name in "api:$API" "idp:$IDP" "exporter:$EXPORTER" "www:$WWW"; do
  label="${name%%:*}"
  url="${name#*:}"
  # No `|| echo 000` fallback: curl already writes 000 for a failed
  # connection, and appending another would produce "000000" and a confusing
  # message. `|| true` keeps `set -e` from taking the script down instead.
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "$url/healthz" 2>/dev/null || true)
  if [ "$code" = "200" ]; then pass "$label /healthz"; else fail "$label /healthz returned $code"; fi
done

step "resource round-trip through the API"
created=$(req PUT "$API/api/v1/vpc/$UID_UNDER_TEST" "$(
  cat <<JSON
{
  "apiVersion": "infra/v1",
  "kind": "vpc",
  "metadata": { "uid": "$UID_UNDER_TEST", "name": "e2e smoke" },
  "spec": { "cidr": "10.240.0.0/16" }
}
JSON
)")
code=$(status_of "$created")
case "$code" in
  200 | 201) pass "PUT created the VPC ($code)" ;;
  *) fail "PUT returned $code: $(body_of "$created")" ;;
esac

got=$(req GET "$API/api/v1/vpc/$UID_UNDER_TEST")
if [ "$(status_of "$got")" = "200" ]; then
  # The CIDR must survive the round-trip through PostgreSQL, not merely be
  # accepted: a serialisation bug in the state backend shows up exactly here.
  if printf '%s' "$(body_of "$got")" | grep -q '10.240.0.0/16'; then
    pass "GET returned the spec as written"
  else
    fail "GET lost the spec: $(body_of "$got")"
  fi
else
  fail "GET returned $(status_of "$got")"
fi

listed=$(req GET "$API/api/v1/vpc")
if printf '%s' "$(body_of "$listed")" | grep -q "$UID_UNDER_TEST"; then
  pass "the VPC appears in the collection"
else
  fail "the VPC is missing from the collection"
fi

step "validation is enforced server-side"
# A client can be wrong; the API must not accept it. This asserts the spec
# Validate() hook is actually wired into the write path.
bad=$(req PUT "$API/api/v1/vpc/${UID_UNDER_TEST}-bad" "$(
  cat <<'JSON'
{
  "apiVersion": "infra/v1",
  "kind": "vpc",
  "metadata": { "uid": "vpc-e2e-bad", "name": "bad" },
  "spec": { "cidr": "not-a-cidr" }
}
JSON
)")
code=$(status_of "$bad")
if [ "$code" = "400" ] || [ "$code" = "422" ]; then
  pass "an invalid CIDR was rejected ($code)"
else
  fail "an invalid CIDR returned $code, expected 400/422"
fi

step "the exporter observes the same state"
metrics=$(curl -sS --max-time 10 "$EXPORTER/metrics" || echo "")
if printf '%s' "$metrics" | grep -q '^infra_'; then
  pass "exporter publishes infra_* series"
else
  fail "exporter published no infra_* series"
fi

step "the identity provider issues and publishes keys"
disco=$(req GET "$IDP/.well-known/openid-configuration")
if [ "$(status_of "$disco")" = "200" ]; then
  pass "OIDC discovery answers"
else
  fail "OIDC discovery returned $(status_of "$disco")"
fi
jwks=$(req GET "$IDP/jwks.json")
if printf '%s' "$(body_of "$jwks")" | grep -q '"keys"'; then
  pass "JWKS publishes a key set"
else
  fail "JWKS returned no key set: $(body_of "$jwks")"
fi

step "the dashboard serves the SPA and proxies the API"
index=$(req GET "$WWW/")
if printf '%s' "$(body_of "$index")" | grep -qi '<!doctype html'; then
  pass "the SPA index is served"
else
  fail "the SPA index was not served"
fi
# The dashboard proxies to the API over a Service. This is the assertion that
# catches a broken GOA_API_URL, which nothing else here would notice.
proxied=$(req GET "$WWW/api/v1/vpc")
if printf '%s' "$(body_of "$proxied")" | grep -q "$UID_UNDER_TEST"; then
  pass "the dashboard proxied the API and saw the VPC"
else
  fail "the dashboard proxy did not return the VPC (status $(status_of "$proxied"))"
fi

step "deletion"
deleted=$(req DELETE "$API/api/v1/vpc/$UID_UNDER_TEST")
code=$(status_of "$deleted")
case "$code" in
  200 | 202 | 204) pass "DELETE accepted ($code)" ;;
  *) fail "DELETE returned $code: $(body_of "$deleted")" ;;
esac
req DELETE "$API/api/v1/vpc/${UID_UNDER_TEST}-bad" > /dev/null 2>&1 || true

echo
if [ "$failures" -gt 0 ]; then
  echo "e2e: $failures check(s) failed" >&2
  exit 1
fi
echo "e2e: all checks passed"
