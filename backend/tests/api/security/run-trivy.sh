#!/usr/bin/env bash
set -euo pipefail

# This script runs Trivy SCA scan for dependency vulnerabilities.
# Requirements: trivy installed

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
mkdir -p "$OUTPUT_DIR"

echo "Running Trivy SCA scan for $PROJECT_ROOT..."

# Scan filesystem for dependency issues
trivy fs --format json --output "$OUTPUT_DIR/trivy-fs-report.json" "$PROJECT_ROOT" || true

echo "Trivy SCA scan complete. Report saved to $OUTPUT_DIR/trivy-fs-report.json"
