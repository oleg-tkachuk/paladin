#!/usr/bin/env bash
# go-mod-tidy.test.sh — every Go module named on the command line (all tracked
# modules when none is) must already be tidy.
#
# The modules are linked: backend and sdk/go/internal/oteltest replace the SDK
# with their working copy, so a dependency the SDK moves moves in their build
# list too. A bump that touches one go.mod and leaves the others behind — a
# dependency bot updating direct requirements only, or a hand-run `go get` in
# one directory — does not fail where it was made. It fails later, in another
# module, as "updates to go.mod needed" or as golangci-lint's "no go files to
# analyze", neither of which names the module that is behind. This check does.
#
# Fix with `task -t Taskfile.dev.yaml deps:tidy`.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly FIX_HINT='task -t Taskfile.dev.yaml deps:tidy'

modules=("$@")
if [[ ${#modules[@]} -eq 0 ]]; then
    while IFS= read -r gomod; do
        modules+=("$(dirname "$gomod")")
    done < <(git ls-files -- 'go.mod' '*/go.mod')
fi

[[ ${#modules[@]} -gt 0 ]] || { echo "!!! no go.mod tracked — this check is asserting nothing" >&2; exit 1; }

fail=0
for dir in "${modules[@]}"; do
    [[ -f "$dir/go.mod" ]] || { echo "!!! $dir has no go.mod" >&2; fail=1; continue; }
    if ! out=$(cd "$dir" && go mod tidy -diff 2>&1); then
        echo "!!! $dir/go.mod is not tidy:" >&2
        echo "$out" >&2
        fail=1
    fi
done

if [[ "$fail" != 0 ]]; then
    echo "!!! run: $FIX_HINT" >&2
    exit 1
fi

echo "go modules: ${#modules[@]} tidy"
