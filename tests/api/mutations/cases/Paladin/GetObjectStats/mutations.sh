#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetObjectStats"

# Mutated 1: Missing tenant_id
PAYLOAD='{}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Unauthenticated
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'
run_test "Mutated: Unauthenticated" "$SERVICE_METHOD" "$PAYLOAD" "Unauthenticated" "Authorization: Bearer invalid"
