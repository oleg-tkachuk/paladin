#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateTenant"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "display_name": "E2E Test Tenant"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Empty tenant_id
PAYLOAD='{
  "tenant_id": "",
  "display_name": "E2E Test Tenant"
}'
run_test "Mutated: Empty tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 3: Unauthenticated
PAYLOAD='{
  "tenant_id": "grpcurl-test-fail-auth",
  "display_name": "Failed Auth Tenant"
}'
run_test "Mutated: Unauthenticated" "$SERVICE_METHOD" "$PAYLOAD" "Unauthenticated" "Authorization: Bearer invalid-token"
