#!/usr/bin/env bash
# publish-plan.test.sh — publish-plan.sh publishes a release at its tag, a dev
# build of every commit CI dispatched, and nothing for a manual run with no tag.
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

check "a tag publishes the release" $'kind=release\nref=v1.2.0' "$("$script" v1.2.0 repository_dispatch abc123)"
check "a manual run's tag publishes the release" $'kind=release\nref=v1.2.0' "$("$script" v1.2.0 workflow_dispatch "")"
check "a CI dispatch publishes its commit" $'kind=dev\nref=abc123' "$("$script" "" repository_dispatch abc123)"
check "a manual run with no tag publishes nothing" "" "$("$script" "" workflow_dispatch abc123 2>/dev/null)"
check "a dispatch naming no commit publishes nothing" "" "$("$script" "" repository_dispatch "" 2>/dev/null)"
if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "publish plan: a release at its tag, a dev build of the tested commit"
