#!/usr/bin/env bash
# ci-groups.sh — which Verify groups a change reaches.
#
# Reads the changed paths on stdin, one per line, and prints the groups as a
# JSON array: `[]` for a change that touches only documentation. The groups are
# the verify-* tasks in Taskfile.dev.yaml, and CI runs one job per group
# printed, so a frontend change does not wait on the backend's tests.
#
# A path feeds the groups whose code reads it. backend builds against
# capability/ and sdk/go/ through `replace`, and every stub is generated from
# proto/. The Taskfiles, their cached includes, the contract scripts and this
# workflow feed every group. Anything else that is not documentation reaches
# `repo`, which checks charts, images, the contract and the repository's own
# config — a path nobody has classified is checked by default, not skipped.

set -euo pipefail

readonly ALL='["backend","capability","sdk","frontend","repo"]'
readonly INERT='(^|/)[^/]*\.md$|^docs/|^LICENSE$|^NOTICE$|^\.gitignore$|\.(png|jpe?g|svg|gif)$'
readonly SHARED='^(Taskfile[^/]*\.yaml$|\.task/|scripts/|\.github/workflows/ci\.yaml$)'

# --all: every group, for a change CI cannot diff (a new branch, a base this
# clone lacks, a run by hand).
if [ "${1:-}" = "--all" ]; then
    printf '%s\n' "$ALL"
    exit 0
fi

# --needs-e2e: given the groups (the JSON array this script prints) on stdin,
# whether the Playwright suite should run. It drives the console against the
# backend, so a change either of them reaches is one it can break.
readonly E2E_GROUPS='["backend","frontend"]'
if [ "${1:-}" = "--needs-e2e" ]; then
    jq -r --argjson e2e "$E2E_GROUPS" 'any(.[]; . as $g | $e2e | index($g) != null)'
    exit 0
fi

relevant="$(grep -vE "$INERT" | grep -v '^$' || true)"
if [ -z "$relevant" ]; then
    printf '%s\n' '[]'
    exit 0
fi

touches() { printf '%s\n' "$relevant" | grep -qE "$1"; }

if touches "$SHARED"; then
    printf '%s\n' "$ALL"
    exit 0
fi

picked=()
if touches '^(backend|capability|sdk/go|proto)/'; then picked+=(backend); fi
if touches '^capability/'; then picked+=(capability); fi
if touches '^(sdk|proto)/'; then picked+=(sdk); fi
if touches '^(frontend|proto)/'; then picked+=(frontend); fi
picked+=(repo)
printf '%s\n' "${picked[@]}" | jq -Rnc '[inputs]'
