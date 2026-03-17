#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/InitiateMultipart"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "content_type": "video/mp4",
  "size_bytes": 104857600
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Negative file size
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "video/mp4",
  "size_bytes": -100
}'
run_test "Mutated: Negative size" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Missing content type
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "size_bytes": 104857600
}'
run_test "Mutated: Missing Content Type" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
