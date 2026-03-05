#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateCategory"

# Mutated 1: Missing slug
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "name": "E2E Test Category"
}'
run_test "Mutated: Missing slug" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Missing name
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "slug": "test-slug-1"
}'
run_test "Mutated: Missing name" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Missing tenant_id
PAYLOAD='{
  "slug": "test-slug-1",
  "name": "E2E Test Category"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
