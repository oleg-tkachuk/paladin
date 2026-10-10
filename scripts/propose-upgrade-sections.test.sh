#!/usr/bin/env bash
# propose-upgrade-sections.test.sh — propose-upgrade-sections.sh opens one
# pull request with the renamed sections, and nothing when there is none.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

script="$(git rev-parse --show-toplevel)/scripts/propose-upgrade-sections.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# gh stands in for GitHub: `pr list` prints the numbers in $OPEN, and every
# call is logged to $CALLS.
mkdir "$work/bin"
cat >"$work/bin/gh" <<'GH'
#!/usr/bin/env bash
echo "$1 $2" >>"$CALLS"
[ "$1 $2" = "pr list" ] && cat "$OPEN"
exit 0
GH
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH" CALLS="$work/calls" OPEN="$work/open"
export REPO=owner/name REMOTE_URL="$work/remote.git" TAGS="v2.0.0 sdk/go/v0.3.0" GH_TOKEN=token
: >"$CALLS"
: >"$OPEN"

git init -q --bare "$REMOTE_URL"
git init -q -b main "$work/repo" && cd "$work/repo"
mkdir docs
printf '## Unreleased — a change\n' >docs/upgrading.md
git add docs && git -c user.name=t -c user.email=t@example.invalid commit -q -m one

failures=0
check() { # name, want, got
    if [ "$2" != "$3" ]; then
        printf 'FAIL %s: want %q, got %q\n' "$1" "$2" "$3"
        failures=$((failures + 1))
    fi
}
branch=docs/upgrading-v2.0.0-sdk/go/v0.3.0
pushed() { git --git-dir="$REMOTE_URL" log -1 --format=%s "$branch" 2>/dev/null || true; }

"$script" >/dev/null
check "nothing renamed pushes nothing" "" "$(pushed)"
check "nothing renamed asks GitHub nothing" "" "$(cat "$CALLS")"

printf '## v2.0.0, sdk/go/v0.3.0 — a change\n' >docs/upgrading.md
GH_TOKEN="" "$script" >/dev/null
check "no token pushes nothing" "" "$(pushed)"

"$script" >/dev/null
check "the renamed sections are pushed" "docs(upgrading): name the sections v2.0.0, sdk/go/v0.3.0 shipped" "$(pushed)"
check "and proposed" "pr list|pr create" "$(paste -sd'|' "$CALLS")"
check "the commit is the guide alone" "docs/upgrading.md" \
    "$(git --git-dir="$REMOTE_URL" diff-tree --no-commit-id --name-only -r "$branch")"

# A re-run of the release: the pull request is already open.
git switch -q main
printf '## v2.0.0, sdk/go/v0.3.0 — a change\n' >docs/upgrading.md
: >"$CALLS"
echo 7 >"$OPEN"
git branch -q -D "$branch"
"$script" >/dev/null
check "an open pull request is not opened again" "pr list" "$(cat "$CALLS")"

if [ "$failures" -gt 0 ]; then
    exit 1
fi
echo "propose-upgrade-sections: every case matches"
