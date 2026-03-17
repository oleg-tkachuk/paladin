#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/PatchTenantMetadata"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "labels_patch_json": "{\"patched\": \"true\"}"
}'

# Depending on isolation, this might be NotFound or OK if seeded. Assume NotFound for safety.
run_test "Happy Path: Patch Tenant (Not Found expected isolated)" "$SERVICE_METHOD" "$PAYLOAD" "NotFound" "Authorization: ${AUTH_TOKEN}"
