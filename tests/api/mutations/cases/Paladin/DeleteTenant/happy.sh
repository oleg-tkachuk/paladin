#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/DeleteTenant"

PAYLOAD='{
  "tenant_id": "test-tenant-to-delete-123"
}'

# Depending on isolation, this might be NotFound or OK if seeded. Assume NotFound for safety.
run_test "Happy Path: Delete Tenant (Not Found expected isolated)" "$SERVICE_METHOD" "$PAYLOAD" "NotFound" "Authorization: ${AUTH_TOKEN}"
