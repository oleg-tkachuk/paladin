#!/usr/bin/env bash
# py-sdk-gen.test.sh — the committed Python stubs must be what the contract
# in proto/ generates today.
#
# Regenerates in place and fails on any difference, the way the Go SDK's
# proto gate does. A stub that lags the contract imports fine and then sends
# a request the server reads with a field missing.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker; buf may reach
# its registry for the contract's dependencies on a cold cache.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly SDK=sdk/python
readonly GENERATED=("$SDK/src/paladin/admin" "$SDK/src/paladin/common" "$SDK/src/paladin/data" "$SDK/src/paladin/iam" "$SDK/src/buf")

for tool in uv buf; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "!!! $tool is not installed; the Python stubs went unchecked" >&2
        exit 1
    }
done

if [[ -z "$(git ls-files -- "${GENERATED[@]}")" ]]; then
    echo "!!! no committed Python stubs under $SDK/src — this check is asserting nothing" >&2
    exit 1
fi

"$SDK/scripts/generate.sh" >/dev/null

# Against the index, not HEAD, so the check also passes on stubs staged for
# the commit being made; a new stub nobody added counts as stale too.
stale=$(git diff --name-status -- "${GENERATED[@]}"; git ls-files --others --exclude-standard -- "${GENERATED[@]}")
if [[ -n "$stale" ]]; then
    {
        echo "!!! the Python stubs are stale — run $SDK/scripts/generate.sh and commit:"
        printf '%s\n' "$stale" | sed 's/^/      /'
    } >&2
    exit 1
fi

echo "python sdk: stubs match the contract"
