#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/CreateObject"

# Happy path 1: Standard object creation
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "image/jpeg",
  "size_bytes": 1024,
  "category": "testcategory",
  "labels": {"source": "grpcurl-test"},
  "external_ref": "ext-123"
}'

run_test "Happy Path: Standard Creation" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"

# Happy path 2: Minimal required fields (implied by service, though some might be required, we'll test without optional ones)
# Note: tenant_id, content_type, size_bytes are usually mandatory
PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "text/plain",
  "size_bytes": 10,
  "category": "testcategory"
}'

run_test "Happy Path: Minimal Fields" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
