#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetCategoryStats"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "slug": "testcategory"
}'

# We expect OK because 'testcategory' was created in the CreateCategory test.
run_test "Happy Path: Default Limit" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
