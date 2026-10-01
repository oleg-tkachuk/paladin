#!/usr/bin/env bash
# new-release-tag.test.sh — new-release-tag.sh names only the tag a run added.
#
# The case that matters is the second dispatch: HEAD already carries the
# release tag from an earlier run, and nothing new was tagged. Reporting that
# tag as new sends release.yaml into `gh release create` for a release that
# exists, which fails the run.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/scripts/new-release-tag.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cd "$work"
git init -q repo && cd repo
git -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m one
before="$work/before"

failures=0
check() { # name, want, got
    if [ "$2" != "$3" ]; then
        printf 'FAIL %s: want %q, got %q\n' "$1" "$2" "$3"
        failures=$((failures + 1))
    fi
}

: >"$before"
check "no tag on HEAD" "" "$("$script" "$before")"

git tag api/v3.0.0
check "a proto baseline is not a release" "" "$("$script" "$before")"

git tag v4.2.0
check "the tag this run pushed" "v4.2.0" "$("$script" "$before")"

git tag --points-at HEAD >"$before"
check "a tag an earlier run pushed" "" "$("$script" "$before")"

git tag v4.2.1
check "a new tag beside an old one" "v4.2.1" "$("$script" "$before")"

if [ "$failures" -gt 0 ]; then
    exit 1
fi
echo "new-release-tag: every case matches"
