#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListObjects"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "limit": 10
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid status
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "status": "not-a-valid-status"
}'
run_test "Mutated: Invalid Status" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Negative Limit
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "limit": -100
}'
run_test "Mutated: Negative Limit" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
