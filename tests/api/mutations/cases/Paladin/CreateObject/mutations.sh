#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateObject"

# Mutated 1: Missing tenant_id (Metadata missing)
PAYLOAD='{
  "content_type": "image/jpeg",
  "size_bytes": 1024
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Missing content_type
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "size_bytes": 1024
}'
run_test "Mutated: Missing content_type" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Negative size_bytes
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "image/jpeg",
  "size_bytes": -500
}'
run_test "Mutated: Negative size_bytes" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 4: Oversized payload (let's assume > 500MB is rejected or size_bytes is huge)
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "image/jpeg",
  "size_bytes": 1000000000000
}'
run_test "Mutated: Oversized size_bytes" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 5: Invalid Auth Token (simulate unauthenticated)
# We test this assuming auth is enforced, if not, it might return OK, but usually it should return Unauthenticated.
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "image/jpeg",
  "size_bytes": 1024
}'
run_test "Mutated: Invalid Auth" "$SERVICE_METHOD" "$PAYLOAD" "Unauthenticated" "Authorization: Bearer invalid-token"

# Mutated 6: Empty strings
PAYLOAD='{
  "tenant_id": "",
  "content_type": "",
  "size_bytes": 1024
}'
run_test "Mutated: Empty strings" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
