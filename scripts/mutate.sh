#!/usr/bin/env bash
# mutate.sh — mutation-test one Go source file with mutate4go, pinned in
# tools/go.mod, against the tests of its own package.
#
#   scripts/mutate.sh backend/internal/api/apiutil/authorize.go
#
# Mutants run one at a time. mutate4go's --max-workers copies only the file's
# module into each worker, where backend's `replace ../sdk/go` points at
# nothing: every mutant then fails to build and is counted as killed.
#
# mutate4go writes its manifest into the file it mutates; the file is put back
# as it was, so nothing it leaves can be committed.
#
# Exits 1 when a mutant survives, 2 on a usage or tool error.

set -euo pipefail

readonly EXIT_SURVIVED=1
readonly EXIT_USAGE=2

if [ $# -ne 1 ] || [ ! -f "$1" ] || [ "${1##*.}" != go ]; then
    echo "usage: $0 <file.go>" >&2
    exit "$EXIT_USAGE"
fi

root=$(cd "$(dirname "$0")/.." && pwd)
readonly TOOLS_MODFILE="$root/tools/go.mod"
file=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")

# The module the file belongs to: the nearest directory with a go.mod.
module=$(dirname "$file")
while [ ! -f "$module/go.mod" ]; do
    if [ "$module" = / ]; then
        echo "mutate: $1 is in no Go module" >&2
        exit "$EXIT_USAGE"
    fi
    module=$(dirname "$module")
done
rel=${file#"$module"/}
pkg=$(dirname "$rel")

saved=$(mktemp)
trap 'cp -f "$saved" "$file"; rm -f "$saved"' EXIT
cp -f "$file" "$saved"

report=$(cd "$module" && go tool -modfile="$TOOLS_MODFILE" mutate4go "$rel" \
    --test-command "go test ./$pkg/" 2>&1) || {
    printf '%s\n' "$report" >&2
    exit "$EXIT_USAGE"
}
printf '%s\n' "$report" | grep -E '^(Killed|Survived|Uncovered):|survived line|^Uncovered mutations|^\[.*uncovered' || true

survived=$(printf '%s\n' "$report" | sed -n 's/^Survived: \([0-9]*\)$/\1/p')
if [ -z "$survived" ]; then
    printf '%s\n' "$report" >&2
    echo "mutate: mutate4go reported no result" >&2
    exit "$EXIT_USAGE"
fi
if [ "$survived" -gt 0 ]; then
    exit "$EXIT_SURVIVED"
fi
