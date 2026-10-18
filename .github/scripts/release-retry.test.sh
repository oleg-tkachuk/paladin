#!/usr/bin/env bash
# release-retry.test.sh — `retry` from .github/scripts/release-retry.sh.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no network.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
# shellcheck source=SCRIPTDIR/release-retry.sh
source "$root/.github/scripts/release-retry.sh"
export RETRY_DELAY_SECONDS=0

work=$(mktemp -d "${TMPDIR:-/tmp}/release-retry-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

# shellcheck disable=SC2329 # invoked through retry, by name
# flaky <failures> <status>: fails the first <failures> calls with <status>,
# then succeeds; counts its calls in $work/calls.
flaky() {
    local n
    n=$(($(cat "$work/calls" 2>/dev/null || echo 0) + 1))
    echo "$n" >"$work/calls"
    [ "$n" -gt "$1" ] || return "$2"
}
calls() { cat "$work/calls"; }
reset() { rm -f "$work/calls"; }

reset
retry flaky 0 1 2>/dev/null || fail "a command that succeeds was reported failed"
[ "$(calls)" = 1 ] || fail "a command that succeeds ran $(calls) times, want 1"

reset
RETRY_ATTEMPTS=4 retry flaky 3 1 2>/dev/null || fail "a command that succeeds on the last attempt was reported failed"
[ "$(calls)" = 4 ] || fail "ran $(calls) times before succeeding on the fourth, want 4"

reset
status=0
RETRY_ATTEMPTS=3 retry flaky 99 7 2>/dev/null || status=$?
[ "$status" = 7 ] || fail "a command failing every attempt returned $status, want its own status 7"
[ "$(calls)" = 3 ] || fail "a command failing every attempt ran $(calls) times, want 3"

reset
status=0
RETRY_ATTEMPTS=1 retry flaky 99 1 2>/dev/null || status=$?
[ "$status" = 1 ] && [ "$(calls)" = 1 ] || fail "one attempt ran $(calls) times, status $status"

[ "$failed" = 0 ] && echo "ok release-retry"
exit "$failed"
