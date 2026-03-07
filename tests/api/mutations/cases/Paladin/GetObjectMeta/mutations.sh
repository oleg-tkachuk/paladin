#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetObjectMeta"

# Mutated 1: Invalid UUID format
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "object_id": "invalid-uuid-123"
}'
run_test "Mutated: Invalid UUID format" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Missing object_id
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Missing object_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
