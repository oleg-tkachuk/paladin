#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetObject"

# Mutated 1: Missing object_id entirely
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Missing object_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid UUID format for object_id
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "object_id": "not-a-uuid"
}'
run_test "Mutated: Invalid UUID format" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Empty tenant_id
PAYLOAD='{
  "tenant_id": "",
  "object_id": "123e4567-e89b-12d3-a456-426614174000"
}'
run_test "Mutated: Empty tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 4: Missing tenant_id field
PAYLOAD='{
  "object_id": "123e4567-e89b-12d3-a456-426614174000"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
