#!/usr/bin/env bash
# propose-upgrade-sections.sh — open a pull request with the upgrade guide's
# sections named by the release that shipped them.
#
# Usage: TAGS="<tags>" GH_TOKEN=<token> REPO=<owner/name> propose-upgrade-sections.sh
#
# release.yaml runs .github/release/name-upgrade-sections.mjs in its working
# tree, which rewrites docs/upgrading.md; this commits that to a branch and
# opens the pull request. main is protected, so a pull request is the only way
# in. GH_TOKEN must be AUTOMERGE_TOKEN, not GITHUB_TOKEN: GitHub starts no
# workflow for a pull request GITHUB_TOKEN opens, so CI would never report and
# the pull request would never merge. auto-merge.yaml queues it once opened.
# The commit is docs, which releases nothing, so merging it cuts no tag.
#
# Safe to re-run: a re-run of a failed release reaches here again. The branch
# is named after the tags and force-pushed, and an open pull request for it is
# left as it is.

set -euo pipefail

readonly UPGRADING=docs/upgrading.md
readonly BRANCH_PREFIX=docs/upgrading-
readonly BOT_NAME='github-actions[bot]'
readonly BOT_EMAIL='41898341+github-actions[bot]@users.noreply.github.com'

tags="${TAGS:?TAGS: the tags this release cut}"
repo="${REPO:?REPO: owner/name}"
# The remote is overridable for the test; release.yaml leaves it to GitHub.
remote="${REMOTE_URL:-https://x-access-token:${GH_TOKEN:-}@github.com/${repo}.git}"

if git diff --quiet -- "$UPGRADING"; then
    echo "no upgrade section names $tags"
    exit 0
fi
if [ -z "${GH_TOKEN:-}" ]; then
    echo "::warning::AUTOMERGE_TOKEN is not set; name the sections for $tags by hand"
    exit 0
fi

branch="${BRANCH_PREFIX}${tags// /-}"
git switch -q -c "$branch"
git add -- "$UPGRADING"
git -c user.name="$BOT_NAME" -c user.email="$BOT_EMAIL" \
    commit -q -m "docs(upgrading): name the sections ${tags// /, } shipped"
# The checkout's own credential is GITHUB_TOKEN, sent as a header to every
# github.com URL; it would win over the token in the URL.
git -c http.https://github.com/.extraheader= push -q --force "$remote" "HEAD:refs/heads/$branch"

if [ -n "$(gh pr list --repo "$repo" --head "$branch" --state open --json number --jq '.[].number')" ]; then
    echo "a pull request for $branch is already open"
    exit 0
fi
gh pr create --repo "$repo" --base main --head "$branch" \
    --title "docs(upgrading): name the sections ${tags// /, } shipped" \
    --body "The release named the upgrade guide's Unreleased sections it shipped."
