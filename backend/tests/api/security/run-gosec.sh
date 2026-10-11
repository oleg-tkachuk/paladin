#!/usr/bin/env bash
set -euo pipefail

# This script runs Gosec (Go Security Checker) to find security problems in Go source code.
# gosec is pinned by the tool directive in backend/go.mod and run through
# `go tool`, so it reads export data with the x/tools that module selects.

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
mkdir -p "$OUTPUT_DIR"

echo "Running Gosec SAST scan for $PROJECT_ROOT..."

cd "$PROJECT_ROOT"
go tool gosec -fmt=json -out="$OUTPUT_DIR/gosec-report.json" -stdout -verbose=true ./... || true

echo "Gosec scan complete. Report saved to $OUTPUT_DIR/gosec-report.json"
