#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/DeleteObject"

# Mutated 1: Invalid UUID format
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "object_id": "not-uuid-struct"
}'
run_test "Mutated: Invalid UUID format" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Missing tenant_id
PAYLOAD='{
  "object_id": "123e4567-e89b-12d3-a456-426614174000"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
