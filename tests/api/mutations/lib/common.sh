#!/usr/bin/env bash

set -eo pipefail

# Setup colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Base directory for all test reports
REPORTS_DIR="$(dirname "${BASH_SOURCE[0]}")/../reports"
RESPONSES_DIR="${REPORTS_DIR}/responses"
LOGS_DIR="${REPORTS_DIR}/logs"

mkdir -p "$RESPONSES_DIR" "$LOGS_DIR"

# Load environment variables if env file exists
if [ -f "$(dirname "${BASH_SOURCE[0]}")/../.env" ]; then
    source "$(dirname "${BASH_SOURCE[0]}")/../.env"
fi

# Apply defaults if not set
export GRPC_ADDR="${GRPC_ADDR:-localhost:9090}"
export GRPC_PLAINTEXT="${GRPC_PLAINTEXT:-true}"
export AUTH_TOKEN="${AUTH_TOKEN:-Bearer test-token}"
export TENANT_ID="${TENANT_ID:-test-tenant-1}"
export REQUEST_ID_PREFIX="${REQUEST_ID_PREFIX:-test-run-}"
export TEST_TIMEOUT_SEC="${TEST_TIMEOUT_SEC:-10}"
export KUBE_NAMESPACE="${KUBE_NAMESPACE:-paladin}"
export KUBE_SELECTOR="${KUBE_SELECTOR:-app.kubernetes.io/name=paladin}"

# Determine grpcurl flags
GRPCURL_FLAGS=("-protoset" "$(dirname "${BASH_SOURCE[0]}")/../../../proto/image.bin")
if [ "$GRPC_PLAINTEXT" = "true" ]; then
    GRPCURL_FLAGS+=("-plaintext")
fi

# Function to run a grpcurl test and verify the response and logs
# Usage: run_test "Test Name" "Service.Method" "JSON_Payload" "Expected_gRPC_Code" [Additional Headers...]
run_test() {
    local test_name="$1"
    local method="$2"
    local payload="$3"
    local expected_code="$4"
    shift 4
    local additional_headers=("$@")

    local req_id="${REQUEST_ID_PREFIX}$(uuidgen | tr '[:upper:]' '[:lower:]')"
    local safe_test_name=$(echo "${method}_${test_name}" | tr ' ' '_' | tr -cd '[:alnum:]_.-')
    local resp_file="${RESPONSES_DIR}/${safe_test_name}.json"
    local log_file="${LOGS_DIR}/${safe_test_name}.log"

    echo -e "\n${YELLOW}Running Test: ${test_name} (${method})${NC}"
    echo "Request ID: ${req_id}"

    # Prepare command
    local cmd=(grpcurl "${GRPCURL_FLAGS[@]}")
    if [ -n "$payload" ]; then
        cmd+=(-d "$payload")
    fi
    
    # Always send Request ID
    cmd+=(-H "x-request-id: ${req_id}")
    
    # Add optional headers
    for target_header in "${additional_headers[@]}"; do
        cmd+=(-H "$target_header")
    done

    cmd+=("${GRPC_ADDR}" "${method}")

    # Run grpcurl and capture output
    # Note: grpcurl output on success is JSON, on error it prints "ERROR:" to stderr and exits non-zero
    set +e
    local output
    output=$("${cmd[@]}" 2>&1)
    local exit_code=$?
    set -e

    echo "$output" > "$resp_file"

    # Analyze actual gRPC code
    local actual_code="OK"
    if [ $exit_code -ne 0 ]; then
        # Parse error code from grpcurl output. Typical format:
        # ERROR:
        #   Code: InvalidArgument
        #   Message: ...
        parsed_code=$(echo "$output" | grep -i "Code:" | awk '{print $2}')
        if [ -n "$parsed_code" ]; then
            actual_code="$parsed_code"
        else
            actual_code="UNKNOWN_ERROR"
        fi
    fi

    # Assert gRPC code
    if [ "$actual_code" != "$expected_code" ]; then
        echo -e "${RED}[FAIL] Expected code: ${expected_code}, Actual code: ${actual_code}${NC}"
        echo "Response: $output"
        return 1
    fi

    echo -e "${GREEN}[PASS] gRPC Status: ${actual_code}${NC}"

    # If we are verifying logs via kubectl, wait briefly for them to flush
    # Try to verify logs if pods are reachable
    if (kubectl -n "$KUBE_NAMESPACE" get pods -l "$KUBE_SELECTOR" --no-headers 2>/dev/null | grep -q .); then
        sleep 1
        
        # Capture logs for this request ID
        kubectl -n "$KUBE_NAMESPACE" logs -l "$KUBE_SELECTOR" --tail=200 > "${LOGS_DIR}/full_current_logs.tmp" || true
        grep "$req_id" "${LOGS_DIR}/full_current_logs.tmp" > "$log_file" || true

        # Check for panics in the request logs
        if grep -i "panic" "$log_file" > /dev/null; then
            echo -e "${RED}[FAIL] Server panic detected in logs for request ${req_id}!${NC}"
            cat "$log_file"
            return 1
        fi
        
        # Count log lines to ensure request was actually processed
        local line_count=$(wc -l < "$log_file" | tr -d ' ')
        if [ "$line_count" -eq 0 ]; then
             echo -e "${YELLOW}[WARN] No logs found for request ${req_id}. Ensure logging is enabled and correlated.${NC}"
        else
             echo -e "${GREEN}[PASS] Log correlation successful (${line_count} lines found). No panics.${NC}"
        fi
    else
        echo -e "${YELLOW}[SKIP] Log verification skipped (kubectl pods not found for selector $KUBE_SELECTOR in ns $KUBE_NAMESPACE)${NC}"
    fi

    return 0
}

export -f run_test
