#!/usr/bin/env bash
set -euo pipefail

# This script runs Trivy SCA scan for dependency vulnerabilities.
# Requirements: trivy installed
#
# trivy exits 0 with findings unless --exit-code says otherwise, so a non-zero
# exit is the scan failing, and fails this script.

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
REPORT="$OUTPUT_DIR/trivy-fs-report.json"
mkdir -p "$OUTPUT_DIR"
rm -f "$REPORT"

echo "Running Trivy SCA scan for $PROJECT_ROOT..."

# Scan filesystem for dependency issues
trivy fs --format json --output "$REPORT" "$PROJECT_ROOT"

echo "Trivy SCA scan complete. Report saved to $REPORT"
