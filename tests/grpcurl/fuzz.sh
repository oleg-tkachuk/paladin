#!/usr/bin/env bash
# fuzz.sh - Simple fuzzer for Paladin gRPC API
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_PATH="${SCRIPT_DIR}/lib/common.sh"

if [ ! -f "${LIB_PATH}" ]; then
    echo "Error: common.sh not found at ${LIB_PATH}"
    exit 1
fi

source "${LIB_PATH}"

# Fuzzing configuration
TENANT_ID="fuzz-tenant"
# Randomly pick an endpoint and send junk/boundary data
ENDPOINTS=(
    "Paladin/BulkCreateObjects"
    "Paladin/BulkDeleteObjects"
    "Paladin/CreateObject"
    "Paladin/GetObject"
    "Paladin/ListObjects"
)

# Junk payloads to test robustness
PAYLOADS=(
    '{}'
    '{"tenant_id": ""}'
    '{"tenant_id": "'$(printf 'A%.0s' {1..5000})'"}'
    '{"object_ids": ["invalid-uuid"]}'
    '{"items": [{"category": "nonexistent"}]}'
    '{"limit": -1}'
    '{"cursor": "---invalid---"}'
)

echo "Starting fuzzing session..."

for i in {1..20}; do
    ENDPOINT=${ENDPOINTS[$RANDOM % ${#ENDPOINTS[@]}]}
    PAYLOAD=${PAYLOADS[$RANDOM % ${#PAYLOADS[@]}]}
    
    echo "[$i] Fuzzing ${ENDPOINT} with payload: ${PAYLOAD:0:100}..."
    
    # We expect many of these to fail with 4xx, but NOT 5xx (Internal)
    # run_test handles grpcurl execution and base validation
    # For fuzzing, we don't strictly check for success, but for LACK of server crashes/500s
    RESPONSE=$(grpcurl -plaintext -d "${PAYLOAD}" localhost:50051 "paladin.${ENDPOINT}" 2>&1 || true)
    
    if echo "${RESPONSE}" | grep -q "Internal"; then
        echo "FAIL: Endpoint ${ENDPOINT} returned Internal error for payload ${PAYLOAD}"
        exit 1
    fi
done

echo "Fuzzing complete. No internal server errors detected."
