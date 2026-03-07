#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateTenant"

# Happy path: Create Tenant
NEW_TENANT="grpcurl-test-tenant-$(date +%s)"

PAYLOAD='{
  "tenant_id": "'"${NEW_TENANT}"'",
  "display_name": "E2E Test Tenant",
  "labels": {"environment": "test"}
}'

# Note: Tenant operations might require special admin privileges depending on implementation.
run_test "Happy Path: Create Tenant" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
