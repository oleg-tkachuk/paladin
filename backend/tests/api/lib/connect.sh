#!/usr/bin/env bash
# Helpers for talking to the Paladin Connect API over plain HTTP/JSON.
#
# All RPCs are POST `/<package>.<Service>/<Method>` with a JSON body.
# Connect over JSON does not need HTTP/2 nor protobuf framing — `curl`
# alone is enough to exercise the entire surface.
set -euo pipefail

PALADIN_HOST="${PALADIN_HOST:-http://127.0.0.1:8080}"

# rpc <Service>/<Method> '<json-body>' [extra-curl-args...]
#
# Reads $JWT and $TENANT_ID from the environment when set; appends the
# Authorization and X-Tenant-ID headers automatically.
#
# Echoes the response body to stdout. Exits non-zero (with curl's stderr
# preserved) if the HTTP status is not 2xx.
rpc() {
    local method="$1"
    local body="$2"
    shift 2

    local hdrs=(
        -H "Content-Type: application/json"
        -H "Connect-Protocol-Version: 1"
    )
    if [[ -n "${JWT:-}" ]]; then
        hdrs+=(-H "Authorization: Bearer ${JWT}")
    fi
    if [[ -n "${TENANT_ID:-}" ]]; then
        hdrs+=(-H "X-Tenant-ID: ${TENANT_ID}")
    fi
    if [[ -n "${IDEMPOTENCY_KEY:-}" ]]; then
        hdrs+=(-H "Idempotency-Key: ${IDEMPOTENCY_KEY}")
    fi

    curl --fail-with-body --silent --show-error \
        --max-time "${PALADIN_TIMEOUT:-15}" \
        "${hdrs[@]}" \
        "$@" \
        --data "$body" \
        "${PALADIN_HOST}/${method}"
}

# rpc_status <Service>/<Method> '<json-body>'
# Like `rpc` but only echoes the HTTP status code. Useful for
# expect-failure assertions.
rpc_status() {
    local method="$1"
    local body="$2"

    local hdrs=(
        -H "Content-Type: application/json"
        -H "Connect-Protocol-Version: 1"
    )
    if [[ -n "${JWT:-}" ]]; then
        hdrs+=(-H "Authorization: Bearer ${JWT}")
    fi
    if [[ -n "${TENANT_ID:-}" ]]; then
        hdrs+=(-H "X-Tenant-ID: ${TENANT_ID}")
    fi

    curl --silent --output /dev/null --write-out '%{http_code}\n' \
        --max-time "${PALADIN_TIMEOUT:-15}" \
        "${hdrs[@]}" \
        --data "$body" \
        "${PALADIN_HOST}/${method}"
}

# Pretty-print a banner for grouping output.
banner() {
    printf '\n────────────────────────────────────────\n%s\n────────────────────────────────────────\n' "$*"
}

# Asserts that `rpc` returned with a key path equal to a value.
#  assert_jq '.objectKey == "logs"' <<<"$resp"
assert_jq() {
    local expr="$1"
    if ! jq -e "$expr" >/dev/null; then
        echo "  ✗ assertion failed: $expr" >&2
        exit 1
    fi
    echo "  ✓ $expr"
}
