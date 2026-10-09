#!/usr/bin/env bash
# stream-release-notes.test.sh — the notes list what the stream released and
# nothing else, in a throwaway repository with this repository's
# .github/release/release.config.cjs.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
work=$(mktemp -d "${TMPDIR:-/tmp}/stream-notes.XXXXXX")
trap 'rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

git -C "$work" init -q
git -C "$work" config user.email test@example.invalid
git -C "$work" config user.name test
mkdir -p "$work/.github/release"
cp "$root/.github/release/release.config.cjs" "$work/.github/release/"
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
commit backend/go.mod "fix(auth): take a limes fix"
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
for unwanted in "a server feature" "explain it" "a limes fix" "the first helper"; do
    [[ "$second" != *"$unwanted"* ]] || fail "the SDK notes list: $unwanted"
done

# A breaking release: one `!` commit, one with the footer, and a guide
# section for the version.
mkdir -p "$work/docs"
printf '# Upgrading\n\n## v0.3.0 — the SDK'"'"'s calls change\n\nText.\n' >"$work/docs/upgrading.md"
commit sdk/go/client.go "feat(sdk)!: rename a helper"
mkdir -p "$work/sdk/go"
echo x >>"$work/sdk/go/other.go"
git -C "$work" add -A
git -C "$work" commit -q -m "fix(sdk): stop retrying a call" -m "BREAKING CHANGE: the call is no longer retried."
commit sdk/go/client.go "feat(sdk): an ordinary helper"
git -C "$work" tag sdk/go/v0.3.0
third=$(notes sdk/go/v0.3.0)
behaviour=${third%%"## Features"*}
for want in "## Behaviour changes" "feat(sdk)!: rename a helper" "fix(sdk): stop retrying a call" \
    "upgrading.md#v030--the-sdks-calls-change"; do
    [[ "$behaviour" == *"$want"* ]] || fail "the breaking notes lack, before the features: $want"
done
features=${third#*"## Features"}
[[ "$features" == *"an ordinary helper"* ]] || fail "the features lack the ordinary helper"
[[ "$features" != *"rename a helper"* ]] || fail "a breaking commit is listed twice"
[[ "$(notes sdk/go/v0.2.0)" != *"Behaviour changes"* ]] || fail "a release with no break has a Behaviour changes section"

if notes v1.0.0 >/dev/null 2>&1; then
    fail "a product tag was given stream notes"
fi

[[ $failed -eq 0 ]] || exit 1
echo "stream release notes: only what the stream released"
