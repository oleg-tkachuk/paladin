#!/usr/bin/env bash
# publish-version-check.test.sh — a release publishes its tag's version, a dev
# build only a dev version.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/.github/scripts/publish-version-check.sh"

failures=0
ok() { # name, args…
    local name=$1
    shift
    "$script" "$@" 2>/dev/null || { printf 'FAIL %s: refused\n' "$name"; failures=$((failures + 1)); }
}
refused() { # name, args…
    local name=$1
    shift
    if "$script" "$@" 2>/dev/null; then
        printf 'FAIL %s: accepted\n' "$name"
        failures=$((failures + 1))
    fi
}

ok "a release at its tag" release 37.1.0 v37.1.0
refused "a release that is not its tag" release 37.1.1 v37.1.0
refused "a release that derived a dev version" release 37.1.1-dev.1792.gabc v37.1.0
ok "a dev build" dev 37.1.1-dev.1792157052.gabcdef12
refused "a dev build that derived a release version" dev 37.1.0
refused "no version" dev ""
refused "an unknown kind" nightly 37.1.1-dev.1.gabc

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "publish version check: a release its tag, a dev build a dev version"
