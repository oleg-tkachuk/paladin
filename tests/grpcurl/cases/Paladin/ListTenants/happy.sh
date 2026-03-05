#!/usr/bin/env bash

set -eo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/../../../lib/common.sh"

SERVICE_METHOD="paladin.v1.Paladin/ListTenants"

PAYLOAD='{
  "limit": 10
}'

run_test "Happy Path: List Tenants with limit" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"

PAYLOAD='{
  "limit": 10,
  "label_selector": {"env": "test"}
}'

run_test "Happy Path: List Tenants with selector" "$SERVICE_METHOD" "$PAYLOAD" "OK" "Authorization: ${AUTH_TOKEN}"
