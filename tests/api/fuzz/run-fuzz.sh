#!/usr/bin/env bash
set -eo pipefail

# This script runs Schemathesis fuzzing against the Paladin API.
# It requires the API to be accessible at the TARGET_URL.

TARGET_URL=${TARGET_URL:-"http://localhost:8080/v1"}
OPENAPI_SPEC=${OPENAPI_SPEC:-"api/openapi.yaml"}
TENANT_ID=${TENANT_ID:-"test-fuzz-tenant"}
MAX_FAILURES=${MAX_FAILURES:-5}

echo "Starting Schemathesis fuzzing against $TARGET_URL..."
echo "Using spec: $OPENAPI_SPEC"

schemathesis run \
  --url "$TARGET_URL" \
  --header "X-Tenant-ID: $TENANT_ID" \
  --header "Authorization: Bearer fuzz-test-token" \
  --max-failures "$MAX_FAILURES" \
  --suppress-health-check all \
  --checks all \
  "$OPENAPI_SPEC"

echo "Fuzzing complete."
