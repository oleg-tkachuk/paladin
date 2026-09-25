#!/usr/bin/env bash
set -euo pipefail

# This script runs OWASP ZAP API scan using Docker.
# Requirements: Docker installed

TARGET_URL=${TARGET_URL:-"http://host.docker.internal:8083"}
OPENAPI_URL=${OPENAPI_URL:-"http://host.docker.internal:8083/v1/openapi.yaml"}
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
mkdir -p "$OUTPUT_DIR"

echo "Running OWASP ZAP API scan against $TARGET_URL..."
echo "Using OpenAPI spec from $OPENAPI_URL"

# Map current reports dir to /zap/wrk in container
# Using zap-api-scan.py for automated API scanning
docker run --rm -v "$OUTPUT_DIR:/zap/wrk:rw" \
    -t ghcr.io/zaproxy/zaproxy:stable zap-api-scan.py \
    -t "$OPENAPI_URL" \
    -f openapi \
    -r zap-report.html || true

echo "OWASP ZAP scan complete. Report saved to $OUTPUT_DIR/zap-report.html"
