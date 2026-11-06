#!/usr/bin/env bash
# go-sdk-consumer.test.sh — scripts/go-sdk-consumer.sh checks the commit it is
# given, from the local clone, and fails on a module graph a consumer cannot
# resolve.
#
# On a throwaway clone: a commit that was never pushed must pass, which only a
# fetch from the clone can do; a commit whose sdk/go/go.mod requires a version
# of a module it imports that nobody serves must fail. (A requirement on a
# module the SDK does not import is pruned from a consumer's graph and never
# fetched, so it is no failure.)
#
# Runs from `task -t Taskfile.dev.yaml verify-go-sdk`. Requires network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/scripts/go-sdk-consumer.sh"
readonly UNSERVED_REQUIREMENT=github.com/oleg-tkachuk/limes@v1.0.0-rc.999999

work=$(mktemp -d "${TMPDIR:-/tmp}/go-sdk-consumer-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

git clone --quiet --shared "$root" "$work/repo"
commit() {
    git -C "$work/repo" -c user.name=test -c user.email=test@example.invalid \
        commit --quiet --allow-empty --no-verify -am "$1"
}

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

commit "test: a commit no remote has"
"$script" "$work/repo" >"$work/unpushed.log" 2>&1 ||
    { cat "$work/unpushed.log"; fail "an unpushed commit with a sound go.mod did not pass"; }

(cd "$work/repo/sdk/go" && go mod edit -require="$UNSERVED_REQUIREMENT")
commit "test: require a version nobody serves"
if "$script" "$work/repo" >"$work/broken.log" 2>&1; then
    cat "$work/broken.log"
    fail "a go.mod requiring $UNSERVED_REQUIREMENT passed"
fi

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "go sdk consumer: an unpushed commit resolves from the clone; an unserved version fails"
