#!/usr/bin/env bash
# frontend-gen.test.sh — the console's committed Connect-ES stubs must be what
# the contract in proto/ generates today.
#
# The stubs embed each file's descriptor, so any contract change — a field, a
# comment, a go_package — changes them. Nothing regenerated them when the
# contract moved, and they sat a step behind with every check green.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker; needs the
# frontend's dependencies installed, which verify-all's frontend steps need too.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly GENERATED=frontend/src/gen

command -v pnpm >/dev/null 2>&1 || {
    echo "!!! pnpm is not installed; the console stubs went unchecked" >&2
    exit 1
}

if [[ -z "$(git ls-files -- "$GENERATED")" ]]; then
    echo "!!! no committed stubs under $GENERATED — this check is asserting nothing" >&2
    exit 1
fi

(cd frontend && pnpm run --silent generate >/dev/null)

# Against the index, so stubs staged for the commit being made pass.
stale=$(git diff --name-status -- "$GENERATED"; git ls-files --others --exclude-standard -- "$GENERATED")
if [[ -n "$stale" ]]; then
    {
        echo "!!! the console stubs are stale — run \`cd frontend && pnpm run generate\` and commit:"
        printf '%s\n' "$stale" | sed 's/^/      /'
    } >&2
    exit 1
fi

echo "console: stubs match the contract"
