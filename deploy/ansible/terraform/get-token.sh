#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>
#
# Fetch a JWT from infra-idp via the OAuth2 client-credentials grant and print
# the access token. Use it to authenticate Terraform / curl against infra-api:
#
#   export GOA_API_TOKEN="$(./get-token.sh)"
#
# Overridable via IDP_URL, CLIENT_ID, CLIENT_SECRET.
set -euo pipefail

IDP_URL="${IDP_URL:-http://192.168.122.10:8081}"
CLIENT_ID="${CLIENT_ID:-terraform}"
CLIENT_SECRET="${CLIENT_SECRET:-$(cat "$(dirname "$0")/../.credentials/terraform-client.secret")}"

curl -fsS -X POST "${IDP_URL}/token" \
  -d grant_type=client_credentials \
  -d "client_id=${CLIENT_ID}" \
  -d "client_secret=${CLIENT_SECRET}" |
  sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'
