#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListCategories"

# Happy Path 1: Default limit
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Happy Path: Default Limit" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"

# Happy Path 2: Explicit limit
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "limit": 5
}'
run_test "Happy Path: Explicit Limit" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
