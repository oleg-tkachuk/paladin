#!/usr/bin/env bash
set -euo pipefail

# Orchestrator for all security tests

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export OUTPUT_DIR="reports/security"

mkdir -p "$OUTPUT_DIR"

echo "========================================"
echo "Starting Full Security Test Suite"
echo "========================================"

"$DIR/run-gosec.sh"
"$DIR/run-trivy.sh"
"$DIR/run-nuclei.sh"
"$DIR/run-zap.sh"

echo "========================================"
echo "Security Test Suite Complete"
echo "Reports available in $OUTPUT_DIR"
echo "========================================"
