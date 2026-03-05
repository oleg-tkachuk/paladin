#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CompleteMultipart"

VALID_UUID="123e4567-e89b-12d3-a456-426614174000"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "upload_id": "'"${VALID_UUID}"'",
  "parts": [
    { "part_number": 1, "etag": "\"etag1\"" },
    { "part_number": 2, "etag": "\"etag2\"" }
  ]
}'

run_test "Happy Path: Valid Structure (Not Found expected isolated)" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
