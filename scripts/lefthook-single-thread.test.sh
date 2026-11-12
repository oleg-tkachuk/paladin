#!/usr/bin/env bash
# lefthook-single-thread.test.sh — the git hooks run their jobs one at a time,
# and every Go job on one thread.
#
# A hook shares the machine with whatever else is building; parallel jobs and
# a multi-threaded go test or lint run beside another build starve both.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly CONFIG="$root/lefthook.yml"
readonly HOOKS=(pre-commit pre-push)
failed=0
fail() {
    printf 'FAIL %s\n' "$1"
    failed=1
}

for hook in "${HOOKS[@]}"; do
    [ "$(yq ".\"$hook\".parallel" "$CONFIG")" = "false" ] || fail "$hook runs its jobs in parallel"
    # Every job that runs the go command pins it to one thread.
    while IFS= read -r job; do
        [ -n "$job" ] || continue
        sel=".\"$hook\".jobs[] | select(.name == \"$job\")"
        [ "$(yq "$sel | .env.GOMAXPROCS" "$CONFIG")" = "1" ] || fail "$hook/$job: GOMAXPROCS is not 1"
        [ "$(yq "$sel | .env.GOFLAGS" "$CONFIG")" = "-p=1" ] || fail "$hook/$job: GOFLAGS is not -p=1"
    done < <(yq ".\"$hook\".jobs[] | select(.run | test(\"(^|\\\\s)(go (test|vet|build)|golangci-lint run)\")) | .name" "$CONFIG")
done

# The Go tests run through go-test-changed.sh, whose default flags hold them to
# one package and one test at a time; the job must leave that default alone.
readonly GO_TEST_JOB=go-test-changed
readonly GO_TEST_SCRIPT=scripts/go-test-changed.sh
readonly SINGLE_THREAD_FLAGS="-p 1 -parallel 1"
go_test=".pre-push.jobs[] | select(.name == \"$GO_TEST_JOB\")"
[[ "$(yq "$go_test | .run" "$CONFIG")" == *"$GO_TEST_SCRIPT"* ]] ||
    fail "$GO_TEST_JOB does not run $GO_TEST_SCRIPT"
[ "$(yq "$go_test | .env.GO_TEST_FLAGS" "$CONFIG")" = "null" ] ||
    fail "$GO_TEST_JOB overrides GO_TEST_FLAGS"
grep -qF ": \"\${GO_TEST_FLAGS:=$SINGLE_THREAD_FLAGS" "$root/$GO_TEST_SCRIPT" ||
    fail "$GO_TEST_SCRIPT does not default to $SINGLE_THREAD_FLAGS"
[ "$(yq "$go_test | .env.GOMAXPROCS" "$CONFIG")" = "1" ] || fail "$GO_TEST_JOB: GOMAXPROCS is not 1"
lint=$(yq '.pre-push.jobs[] | select(.name == "backend-golangci") | .run' "$CONFIG")
[[ "$lint" == *"--concurrency=1"* ]] || fail "backend-golangci runs on more than one thread"

[ "$failed" -eq 0 ] || exit 1
printf '%s\n' "lefthook: hooks run sequentially, Go jobs single-threaded"
