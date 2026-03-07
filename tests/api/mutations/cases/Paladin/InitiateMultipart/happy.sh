#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/InitiateMultipart"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'",
  "content_type": "application/octet-stream",
  "size_bytes": 10485760,
  "category": "testcategory",
  "labels": {"source": "grpcurl-test"},
  "external_ref": "large-ext-123"
}'

run_test "Happy Path: Standard Initiate" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
