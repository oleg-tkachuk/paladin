#!/usr/bin/env bash
# Top-level test orchestrator. Runs the full test pyramid against a
# live PALADIN backend:
#
#   1. health probes        (zero auth, sub-second)
#   2. functional smoke     (every Connect service, realistic flows)
#   3. e2e hurl suite       (declarative, captures + asserts response shape)
#   4. fuzz                 (negative-input batter against validate-go rules)
#
# Quit early on the first failure. Each stage prints its own banner so a
# skim of CI logs lands on the failing one.
#
# Usage
#   tests/api/run-all.sh
#
# Skip individual stages with env vars:
#   SKIP_FUZZ=1 tests/api/run-all.sh
#   SKIP_HURL=1 tests/api/run-all.sh
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PALADIN_HOST="${PALADIN_HOST:-http://127.0.0.1:8080}"

stage() {
    printf '\n════════════════════════════════════════\n%s\n════════════════════════════════════════\n' "$*"
}

# Quick liveness check first — pointless to fan out tests against a dead
# backend.
stage "0. Liveness ping"
if ! curl --silent --fail --max-time 5 "$PALADIN_HOST/livez" >/dev/null; then
    echo "PALADIN not reachable at $PALADIN_HOST — set PALADIN_HOST or start the server." >&2
    exit 1
fi
echo "  ✓ $PALADIN_HOST/livez"

stage "1. Functional smoke (every service)"
"$DIR/functional/run-functional.sh"

if [[ "${SKIP_HURL:-}" != "1" ]]; then
    stage "2. Hurl e2e suite"
    if ! command -v hurl >/dev/null; then
        echo "  ⚠ skipping: hurl not installed (set SKIP_HURL=1 to silence)" >&2
    else
        "$DIR/e2e/run-e2e.sh"
    fi
fi

if [[ "${SKIP_FUZZ:-}" != "1" ]]; then
    stage "3. Fuzz harness"
    "$DIR/fuzz/run-fuzz.sh"
fi

stage "ALL TEST STAGES PASSED"
