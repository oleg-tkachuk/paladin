#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListObjects"

# Happy Path 1: Default List
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Happy Path: Default List" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"

# Happy Path 2: Explicit Limit and filter
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "limit": 10,
  "status": "complete"
}'
run_test "Happy Path: Limit and Filter" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
