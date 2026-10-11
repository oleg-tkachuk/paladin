#!/usr/bin/env bash
set -euo pipefail

# This script runs OWASP ZAP API scan using Docker.
# Requirements: Docker installed
#
# zap-api-scan.py reports findings through its exit code; those are the
# report's content and do not fail this script. Its own failure, and Docker's
# (125-127), do.

# zap-api-scan.py's exit codes: 0 passed, 1 at least one FAIL, 2 WARNs only.
# Anything else (3 is its own exception) means there is no scan to report.
readonly ZAP_EXIT_PASS=0
readonly ZAP_EXIT_FAIL=1
readonly ZAP_EXIT_WARN=2

TARGET_URL=${TARGET_URL:-"http://host.docker.internal:8083"}
OPENAPI_URL=${OPENAPI_URL:-"http://host.docker.internal:8083/v1/openapi.yaml"}
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
REPORT_NAME="zap-report.html"
mkdir -p "$OUTPUT_DIR"
rm -f "$OUTPUT_DIR/$REPORT_NAME"

echo "Running OWASP ZAP API scan against $TARGET_URL..."
echo "Using OpenAPI spec from $OPENAPI_URL"

# Map current reports dir to /zap/wrk in container
# Using zap-api-scan.py for automated API scanning
status=0
docker run --rm -v "$OUTPUT_DIR:/zap/wrk:rw" \
    -t ghcr.io/zaproxy/zaproxy:stable zap-api-scan.py \
    -t "$OPENAPI_URL" \
    -f openapi \
    -r "$REPORT_NAME" || status=$?

case "$status" in
"$ZAP_EXIT_PASS" | "$ZAP_EXIT_FAIL" | "$ZAP_EXIT_WARN") ;;
*)
    printf 'ZAP did not complete a scan (exit %d)\n' "$status" >&2
    exit 1
    ;;
esac
if [ ! -s "$OUTPUT_DIR/$REPORT_NAME" ]; then
    printf 'ZAP wrote no report at %s\n' "$OUTPUT_DIR/$REPORT_NAME" >&2
    exit 1
fi

echo "OWASP ZAP scan complete. Report saved to $OUTPUT_DIR/$REPORT_NAME"
