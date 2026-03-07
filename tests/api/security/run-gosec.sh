#!/usr/bin/env bash
set -euo pipefail

# This script runs Gosec (Go Security Checker) to find security problems in Go source code.
# Requirements: gosec installed (go install github.com/securego/gosec/v2/cmd/gosec@latest)

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
mkdir -p "$OUTPUT_DIR"

echo "Running Gosec SAST scan for $PROJECT_ROOT..."

# Exclude vendor and tests directories from scan
gosec -fmt=json -out="$OUTPUT_DIR/gosec-report.json" -stdout -verbose=true "$PROJECT_ROOT/..." || true

echo "Gosec scan complete. Report saved to $OUTPUT_DIR/gosec-report.json"
