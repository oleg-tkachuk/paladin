#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/GetObjectStats"

PAYLOAD='{
  "tenant_id": "'"${TENANT_ID}"'"
}'

run_test "Happy Path: Valid Request" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
