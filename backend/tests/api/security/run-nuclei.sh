#!/usr/bin/env bash
set -euo pipefail

# This script runs Nuclei DAST scan against the running API.
# Requirements: nuclei installed (go install -v github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest)

TARGET_URL=${TARGET_URL:-"http://localhost:8083"}
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
mkdir -p "$OUTPUT_DIR"

echo "Running Nuclei DAST scan against $TARGET_URL..."

# Running with default templates, filtering for relevant ones if needed
nuclei -u "$TARGET_URL" -o "$OUTPUT_DIR/nuclei-report.txt" -v || true

echo "Nuclei scan complete. Report saved to $OUTPUT_DIR/nuclei-report.txt"
