#!/usr/bin/env bash
# go-module-paths.test.sh — every Go module's path must be the repository path
# plus the directory it sits in.
#
# Go resolves a module path to a repository and a subdirectory. A go.mod in
# backend/ declaring the repository root builds fine from inside the tree and
# cannot be fetched by anyone outside it: the proxy looks for a go.mod at the
# root, finds none, and reports the package missing.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly RELEASE_CONFIG=release.config.cjs

repo=$(sed -n 's|.*repositoryUrl: *"https://\(github.com/[^"]*\)\.git".*|\1|p' "$RELEASE_CONFIG")
[[ -n "$repo" ]] || { echo "!!! no repositoryUrl in $RELEASE_CONFIG" >&2; exit 1; }

fail=0
count=0
while IFS= read -r gomod; do
    dir=$(dirname "$gomod")
    want="$repo/$dir"
    [[ "$dir" == "." ]] && want="$repo"
    got=$(sed -n 's/^module[[:space:]]*//p' "$gomod" | head -1)
    count=$((count + 1))
    if [[ "$got" != "$want" ]]; then
        echo "!!! $gomod declares '$got', its directory makes it '$want'" >&2
        fail=1
    fi
done < <(git ls-files -- 'go.mod' '*/go.mod')

[[ "$count" -gt 0 ]] || { echo "!!! no go.mod tracked — this check is asserting nothing" >&2; exit 1; }
[[ "$fail" == 0 ]] || exit 1

echo "go modules: $count paths match their directories"
