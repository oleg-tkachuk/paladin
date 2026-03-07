#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/SignPart"

VALID_UUID="123e4567-e89b-12d3-a456-426614174000"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'",
  "part_number": 1
}'

# If the upload ID isn't real, it should be NotFound or InvalidArgument depending on implementation.
run_test "Happy Path: Valid Structure (Not Found expected isolated)" "$SERVICE_METHOD" "$PAYLOAD" "NotFound" "Authorization: ${AUTH_TOKEN}"
