#!/usr/bin/env bash
# publish-plan.test.sh — publish-plan.sh publishes a release at its tag, a dev
# build of the tested commit, and nothing for an untested head.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/.github/scripts/publish-plan.sh"

failures=0
check() { # name, want, got
    if [ "$2" != "$3" ]; then
        printf 'FAIL %s: want %q, got %q\n' "$1" "$2" "$3"
        failures=$((failures + 1))
    fi
}

check "a tag publishes the release" $'kind=release\nref=v1.2.0' "$("$script" v1.2.0 true abc123)"
check "a tag wins over a dev build" $'kind=release\nref=v1.2.0' "$("$script" v1.2.0 false "")"
check "no tag publishes the tested commit" $'kind=dev\nref=abc123' "$("$script" "" true abc123)"
check "an untested head publishes nothing" "" "$("$script" "" false abc123 2>/dev/null)"
check "no tested sha publishes nothing" "" "$("$script" "" true "" 2>/dev/null)"

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "publish plan: a release at its tag, a dev build of the tested commit"
