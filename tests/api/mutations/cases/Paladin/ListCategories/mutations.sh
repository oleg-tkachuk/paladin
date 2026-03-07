#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListCategories"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "limit": 10
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid negative limit
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "limit": -10
}'
run_test "Mutated: Negative limit" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Unauthenticated
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Invalid Auth" "$SERVICE_METHOD" "$PAYLOAD" "Unauthenticated" "Authorization: Bearer invalid-token"
