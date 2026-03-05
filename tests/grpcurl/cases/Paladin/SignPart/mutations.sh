#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/SignPart"

VALID_UUID="123e4567-e89b-12d3-a456-426614174000"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "upload_id": "'"${VALID_UUID}"'",
  "part_number": 1
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid part number 0
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'",
  "part_number": 0
}'
run_test "Mutated: Invalid Part Number 0" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Invalid part number negative
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'",
  "part_number": -5
}'
run_test "Mutated: Invalid Part Number Negative" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
