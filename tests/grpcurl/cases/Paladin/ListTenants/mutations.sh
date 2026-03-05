#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListTenants"

# Mutated 1: Negative limit
PAYLOAD='{
  "limit": -5
}'
run_test "Mutated: Negative limit" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid cursor (e.g. malformed RFC3339)
PAYLOAD='{
  "limit": 10,
  "cursor": "not-a-timestamp"
}'
run_test "Mutated: Invalid Cursor" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
