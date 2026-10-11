#!/usr/bin/env bash
set -euo pipefail

# This script runs Gosec (Go Security Checker) to find security problems in Go source code.
# gosec is pinned by the tool directive in backend/go.mod and run through
# `go tool`, so it reads export data with the x/tools that module selects.
#
# Findings are the report's content and do not fail the script; a scan that
# did not happen does. gosec exits 1 both for findings and for packages it
# could not load, so whether it scanned is read from the report.

# gosec's exit codes: 0 nothing found, 1 findings or load errors. Any other
# code is the tool not running at all (`go tool` without gosec, a crash).
readonly GOSEC_EXIT_CLEAN=0
readonly GOSEC_EXIT_FINDINGS=1
# The report's key for the packages gosec could not load or type-check.
readonly GOSEC_LOAD_ERRORS_KEY="Golang errors"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/reports/security"
REPORT="$OUTPUT_DIR/gosec-report.json"
mkdir -p "$OUTPUT_DIR"
# A report left by an earlier run would pass for this one.
rm -f "$REPORT"

echo "Running Gosec SAST scan for $PROJECT_ROOT..."

cd "$PROJECT_ROOT"
status=0
go tool gosec -fmt=json -out="$REPORT" -stdout -verbose=true ./... || status=$?

if [ "$status" -ne "$GOSEC_EXIT_CLEAN" ] && [ "$status" -ne "$GOSEC_EXIT_FINDINGS" ]; then
    printf 'gosec did not run (exit %d)\n' "$status" >&2
    exit 1
fi
if ! jq -e 'type == "object"' "$REPORT" >/dev/null 2>&1; then
    printf 'gosec wrote no readable report at %s\n' "$REPORT" >&2
    exit 1
fi
load_errors=$(jq --arg key "$GOSEC_LOAD_ERRORS_KEY" '.[$key] // {} | length' "$REPORT")
if [ "$load_errors" -ne 0 ]; then
    printf 'gosec could not load %d package(s); see "%s" in %s\n' \
        "$load_errors" "$GOSEC_LOAD_ERRORS_KEY" "$REPORT" >&2
    exit 1
fi

echo "Gosec scan complete. Report saved to $REPORT"
