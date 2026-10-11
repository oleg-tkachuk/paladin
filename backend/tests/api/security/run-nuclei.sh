#!/usr/bin/env bash
set -euo pipefail

# This script runs Nuclei DAST scan against the running API.
# Requirements: nuclei installed (go install -v github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest)
#
# nuclei exits 0 with findings, so a non-zero exit is the scan failing, and
# fails this script.

TARGET_URL=${TARGET_URL:-"http://localhost:8083"}
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
REPORT="$OUTPUT_DIR/nuclei-report.txt"
mkdir -p "$OUTPUT_DIR"
rm -f "$REPORT"

echo "Running Nuclei DAST scan against $TARGET_URL..."

# Running with default templates, filtering for relevant ones if needed
nuclei -u "$TARGET_URL" -o "$REPORT" -v

echo "Nuclei scan complete. Report saved to $REPORT"
