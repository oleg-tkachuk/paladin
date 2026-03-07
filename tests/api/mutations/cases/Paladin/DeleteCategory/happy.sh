#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/DeleteCategory"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "slug": "testcategory-to-delete"
}'

# Depending on implementation, deleting non-existent might be OK or NotFound. Treat as NotFound for isolation if strict.
run_test "Happy Path: Valid Structure (Not Found expected isolated)" "$SERVICE_METHOD" "$PAYLOAD" "NotFound" "Authorization: ${AUTH_TOKEN}"
