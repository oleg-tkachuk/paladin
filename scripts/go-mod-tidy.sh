#!/usr/bin/env bash
# go-mod-tidy.sh — tidy every tracked Go module, the SDK first.
#
# backend and sdk/go/internal/oteltest replace the SDK with this tree and take
# its requirements into their own build lists, so the SDK is tidied before
# them; the rest follow in path order. scripts/go-mod-tidy.test.sh checks the
# result.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

readonly SDK_MODULE=sdk/go

modules=("$SDK_MODULE")
while IFS= read -r gomod; do
    dir=$(dirname "$gomod")
    [[ "$dir" == "$SDK_MODULE" ]] || modules+=("$dir")
done < <(git ls-files -- 'go.mod' '*/go.mod' | sort)

[[ -f "$SDK_MODULE/go.mod" ]] || { echo "!!! $SDK_MODULE/go.mod is gone; update SDK_MODULE" >&2; exit 1; }

for dir in "${modules[@]}"; do
    echo "--- go mod tidy: $dir"
    (cd "$dir" && go mod tidy)
done
