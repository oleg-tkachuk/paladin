#!/usr/bin/env bash
# go-fuzz.sh — run every native fuzz target of one or more Go modules, each for
# a bounded time.
#
#   scripts/go-fuzz.sh <fuzztime> <module dir>...
#
# <fuzztime> is go test's -fuzztime: a duration ("2m") or a count ("500x").
# `go test -fuzz` takes one target of one package per run, so the targets are
# found, not listed: every package holding a `func Fuzz` in a test file, and
# every target `go test -list` reports there. A target that fails does not
# stop the rest, so one crash cannot hide another; the exit status is non-zero
# when any failed. A failing input is written where go test reads it back,
# <package>/testdata/fuzz/<target>/, and reproduces with
# `go test -run=<target>/<file> ./<package>`.
#
# The seed corpora run in every plain `go test`; this is the time-bounded
# search for new inputs, run nightly (.github/workflows/fuzz.yaml).

set -euo pipefail

readonly EXIT_USAGE=2
readonly FUZZ_FUNC='^func Fuzz'
readonly FUZZ_LIST='^Fuzz'

if [ $# -lt 2 ]; then
    echo "usage: $0 <fuzztime> <module dir>..." >&2
    exit "$EXIT_USAGE"
fi
fuzztime=$1
shift

failed=()
ran=0
for module in "$@"; do
    # grep exits 1 for a module with no target, which is not this step failing.
    files=$(cd "$module" && find . -name '*_test.go' -not -path '*/testdata/*' -exec grep -l "$FUZZ_FUNC" {} + || true)
    packages=$(for f in $files; do dirname "$f"; done | sort -u)
    for pkg in $packages; do
        # A package whose tests do not build is a failure, not a package
        # without targets.
        if ! listing=$(cd "$module" && go test -list "$FUZZ_LIST" "$pkg"); then
            failed+=("$module $pkg: its tests do not build")
            continue
        fi
        targets=$(printf '%s\n' "$listing" | grep "$FUZZ_LIST" || true)
        for target in $targets; do
            ran=$((ran + 1))
            echo "go-fuzz: $module $pkg $target for $fuzztime"
            if ! (cd "$module" && go test -run='^$' -fuzz="^${target}\$" -fuzztime="$fuzztime" "$pkg"); then
                failed+=("$module $pkg $target")
            fi
        done
    done
done

if [ "${#failed[@]}" -gt 0 ]; then
    echo "go-fuzz: ${#failed[@]} failed of $ran targets run:" >&2
    printf '  %s\n' "${failed[@]}" >&2
    exit 1
fi
# A module with no target left is a discovery that stopped working, not a
# clean run.
if [ "$ran" -eq 0 ]; then
    echo "go-fuzz: no fuzz target found in $*" >&2
    exit 1
fi
echo "go-fuzz: $ran targets ran for $fuzztime each, none failed"
