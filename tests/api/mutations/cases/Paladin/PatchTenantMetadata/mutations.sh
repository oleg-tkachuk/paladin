#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/PatchTenantMetadata"

# Mutated 1: Missing tenant_id
PAYLOAD='{
  "labels_patch_json": "{\"patched\": \"true\"}"
}'
run_test "Mutated: Missing tenant_id" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"

# Mutated 2: Invalid JSON patch syntax
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "labels_patch_json": "{invalid-json}"
}'
run_test "Mutated: Invalid Patch JSON" "$SERVICE_METHOD" "$PAYLOAD" "InvalidArgument" "Authorization: ${AUTH_TOKEN}"
