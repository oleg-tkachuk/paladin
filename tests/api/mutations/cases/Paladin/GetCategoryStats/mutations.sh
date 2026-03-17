#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetCategoryStats"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "slug": "testcategory"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Missing slug
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Missing slug" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
