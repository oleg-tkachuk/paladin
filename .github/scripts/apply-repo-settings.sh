#!/usr/bin/env bash
#
# apply-repo-settings.sh — repository settings that live in the GitHub API,
# not in the codebase. Idempotent; safe to re-run.
#
# These cannot be applied from a sandboxed coding session (no GitHub auth), so
# they live here as version-controlled, reviewable configuration. Run once from
# a terminal that is authenticated as a repo admin:
#
#     gh auth login            # or: export GH_TOKEN=<PAT with 'repo' + admin>
#     .github/scripts/apply-repo-settings.sh
#
# What it does:
#   1. Leaves repository VISIBILITY untouched — the repo stays private. The
#      current value is only printed, never changed.
#   2. Turns OFF "automatically delete head branches" on merge, so a
#      develop -> main PR merge never auto-removes develop.
#   3. Creates/updates a branch RULESET that forbids DELETING main and develop.
#      A ruleset is used instead of classic branch protection because it is
#      ADDITIVE — it will not overwrite any protection already configured on
#      main — and it is available on free private repositories.
#
# Combined effect: after a develop -> main PR merge GitHub neither auto-deletes
# develop nor offers a working "Delete branch" button for it, and neither
# branch can be removed by accident.
#
# The ruleset intentionally does NOT require pull requests, reviews, or status
# checks, so the existing "push straight to develop" workflow is unaffected.

set -euo pipefail

REPO="${REPO:-oleg-tkachuk/paladin}"
RULESET_NAME="protect-main-develop"

command -v gh >/dev/null || { echo "error: gh CLI not found" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "error: gh is not authenticated (run: gh auth login)" >&2; exit 1; }

echo "Repository: $REPO"
echo "Visibility (left unchanged): $(gh api "repos/$REPO" --jq .visibility)"

echo "==> Disabling auto-delete of head branches on merge"
gh api -X PATCH "repos/$REPO" -F delete_branch_on_merge=false \
  --jq '"    delete_branch_on_merge = " + (.delete_branch_on_merge | tostring)'

echo "==> Ensuring deletion-protection ruleset for main + develop"
# To also block force-pushes (history rewrites) on these branches, add
#   { "type": "non_fast_forward" }
# to the rules array below.
payload=$(cat <<'JSON'
{
  "name": "protect-main-develop",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/main", "refs/heads/develop"],
      "exclude": []
    }
  },
  "rules": [
    { "type": "deletion" }
  ]
}
JSON
)

existing_id=$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name==\"$RULESET_NAME\") | .id" 2>/dev/null | head -n1 || true)
if [ -n "$existing_id" ]; then
  echo "    updating existing ruleset #$existing_id"
  printf '%s' "$payload" | gh api -X PUT "repos/$REPO/rulesets/$existing_id" --input - \
    --jq '"    " + .name + " -> " + .enforcement'
else
  echo "    creating ruleset"
  printf '%s' "$payload" | gh api -X POST "repos/$REPO/rulesets" --input - \
    --jq '"    " + .name + " -> " + .enforcement'
fi

echo "Done."
