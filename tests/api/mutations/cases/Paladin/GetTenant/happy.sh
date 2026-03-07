#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetTenant"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'

# Depending on if TENANT_ID exists, this will be OK or NotFound. We expect OK if seeded properly or NotFound in isolation.
run_test "Happy Path: Existing Tenant (or Not Found)" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}" || \
run_test "Happy Path: Existing Tenant (Fallback NotFound)" "$SERVICE_METHOD" "$PAYLOAD" "NotFound" "Authorization: ${AUTH_TOKEN}"
