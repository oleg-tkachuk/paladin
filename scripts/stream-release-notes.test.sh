#!/usr/bin/env bash
# stream-release-notes.test.sh — the notes list what the stream released and
# nothing else, in a throwaway repository with this repository's
# release.config.cjs.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
work=$(mktemp -d "${TMPDIR:-/tmp}/stream-notes.XXXXXX")
trap 'rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

git -C "$work" init -q
git -C "$work" config user.email test@example.invalid
git -C "$work" config user.name test
cp "$root/release.config.cjs" "$work/"
commit() { # commit <path> <message>
    mkdir -p "$work/$(dirname "$1")"
    echo "$2" >>"$work/$1"
    git -C "$work" add -A
    git -C "$work" commit -q -m "$2"
}

commit sdk/go/client.go "feat(sdk): the first helper"
git -C "$work" tag sdk/go/v0.1.0
commit sdk/go/client.go "feat(sdk): a second helper"
commit proto/api.proto "feat(events): a new RPC"
commit backend/main.go "feat(api): a server feature"
commit sdk/python/client.py "fix(sdk): a python fix"
commit sdk/go/README.md "docs(sdk): explain it"
commit capability/mint.go "fix(capability): a module fix"
git -C "$work" tag sdk/go/v0.2.0
git -C "$work" tag v1.0.0

notes() { RELEASE_ROOT="$work" GITHUB_REPOSITORY=o/r "$root/scripts/stream-release-notes.sh" "$1"; }

first=$(notes sdk/go/v0.1.0)
[[ "$first" == "SDK 0.1.0"* && "$first" == *"first release"* ]] || fail "the first tag: $first"

second=$(notes sdk/go/v0.2.0)
for want in "SDK 0.2.0" "feat(sdk): a second helper" "feat(events): a new RPC" "fix(sdk): a python fix" \
    "compare/sdk/go/v0.1.0...sdk/go/v0.2.0"; do
    [[ "$second" == *"$want"* ]] || fail "the SDK notes lack: $want"
done
for unwanted in "a server feature" "explain it" "a module fix" "the first helper"; do
    [[ "$second" != *"$unwanted"* ]] || fail "the SDK notes list: $unwanted"
done

if notes v1.0.0 >/dev/null 2>&1; then
    fail "a product tag was given stream notes"
fi

[[ $failed -eq 0 ]] || exit 1
echo "stream release notes: only what the stream released"
