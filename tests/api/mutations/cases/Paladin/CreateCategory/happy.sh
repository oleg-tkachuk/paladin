#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateCategory"

# Unique slug for happy path to avoid conflicts
SLUG="test-category-$(date +%s)"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "slug": "'"${SLUG}"'",
  "name": "E2E Test Category",
  "description": "Created by grpcurl e2e tests"
}'

run_test "Happy Path: Create Category" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
