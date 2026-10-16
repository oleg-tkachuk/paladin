#!/usr/bin/env bash
# release-ci-check.test.sh — release-ci-check.sh releases a commit only once a
# CI run on main passed for it.
#
# gh is a stub on PATH that answers with the conclusions the case sets, so
# this runs from `task -t Taskfile.dev.yaml verify-all` with no network.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/.github/scripts/release-ci-check.sh"
stub="$(mktemp -d "${TMPDIR:-/tmp}/release-ci-check.XXXXXX")"
trap 'rm -rf "$stub"' EXIT

cat >"$stub/gh" <<'GH'
#!/usr/bin/env bash
# Records the query, answers with the case's conclusions, one per line.
printf '%s\n' "$*" >"$STUB_DIR/query"
printf '%b' "$CONCLUSIONS"
GH
chmod +x "$stub/gh"
export PATH="$stub:$PATH" STUB_DIR="$stub" GITHUB_REPOSITORY=owner/repo

failures=0
check() { # name, want, conclusions
    local got
    got=$(CONCLUSIONS="$3" "$script" abc123 2>/dev/null)
    if [ "$2" != "$got" ]; then
        printf 'FAIL %s: want %q, got %q\n' "$1" "$2" "$got"
        failures=$((failures + 1))
    fi
}

check "a run that passed" "release=true" 'success\n'
check "a re-run that passed after a failure" "release=true" 'failure\nsuccess\n'
check "a run that failed" "release=false" 'failure\n'
check "a run still in progress" "release=false" '\n'
check "no run at all" "release=false" ''
check "a skipped run is not a pass" "release=false" 'skipped\n'

CONCLUSIONS='success\n' "$script" abc123 >/dev/null 2>&1
query=$(cat "$stub/query")
case "$query" in
    *"head_sha=abc123"*"event=push"*"branch=main"*) ;;
    *) printf 'FAIL the query names the commit, push and main: %s\n' "$query"; failures=$((failures + 1)) ;;
esac

if "$script" "" >/dev/null 2>&1; then
    printf 'FAIL %s\n' "an empty sha is accepted"
    failures=$((failures + 1))
fi

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "release CI check: releases a commit only once CI on main passed"
