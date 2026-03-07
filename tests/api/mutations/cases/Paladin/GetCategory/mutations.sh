#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetCategory"

# Mutated 1: Missing slug
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Missing slug" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid slug format (e.g. spaces if validated)
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "slug": "invalid slug with spaces"
}'
# Assuming validation catches this
run_test "Mutated: Invalid slug format" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Missing tenant_id
PAYLOAD='{
  "slug": "testcategory"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
