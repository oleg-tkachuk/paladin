#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CompleteMultipart"

VALID_UUID="123e4567-e89b-12d3-a456-426614174000"

# Mutated 1: Missing parts array
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'"
}'
# Usually parts are required, might be InvalidArgument
run_test "Mutated: Missing parts" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Empty parts array
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'",
  "parts": []
}'
run_test "Mutated: Empty parts array" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Missing tenant_id
PAYLOAD='{
  "upload_id": "'"${VALID_UUID}"'",
  "parts": [{ "part_number": 1, "etag": "\"etag1\"" }]
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
