#!/usr/bin/env bash
#
# apply-repo-settings.sh — repository settings that live in the GitHub API,
# not in the codebase. Idempotent; safe to re-run.
#
# These cannot be applied from a sandboxed coding session (no GitHub auth), so
# they live here as version-controlled, reviewable configuration. Run from a
# terminal authenticated as a repo admin:
#
#     gh auth login            # or: export GH_TOKEN=<PAT with 'repo' + admin>
#     .github/scripts/apply-repo-settings.sh
#
# What it does:
#   1. Leaves repository VISIBILITY untouched — flipping a repo public is a
#      one-way door and belongs to a human, not a script. The current value
#      is printed so you know which profile the rest of the run assumes.
#   2. Turns OFF "automatically delete head branches" on merge, so a merged
#      pull request's branch stays until its author removes it.
#   3. Enables private vulnerability reporting and Dependabot alerts. Both
#      are free on public repositories, and SECURITY.md points contributors
#      at the private-reporting form — a form that 404s if this is not on.
#   4. Creates/updates a branch RULESET protecting main, the only long-lived
#      branch (trunk-based: every change reaches it through a pull request).
#
# Rulesets rather than classic branch protection: they are additive (they
# never clobber protection already configured elsewhere) and they work on
# free repositories, public or private.
#
# REQUIRED STATUS CHECKS
#
# Off by default, because a required check that never reports blocks every
# merge — and this repository has been in exactly that state, with Actions
# runs failing at startup on an exhausted private-repo minutes allowance.
#
# Public repositories get Actions minutes for free, so once runs are green
# again, turn them on:
#
#     REQUIRE_CHECKS=1 .github/scripts/apply-repo-settings.sh
#
# They land on `main`, in a ruleset of their own. A required check on a branch
# blocks the direct pushes to it as well as the merges: the commit being pushed
# has no check runs yet, so there is nothing for the rule to pass, and the only
# route left is a pull request — which is how trunk-based work reaches main.
# Override with CHECKS_REFS if that stops being true.
#
# Verify a run has actually gone green on each context first — the names
# below are the check-run names GitHub reports, which are the jobs' `name:`
# in .github/workflows/ci.yaml, not the workflow name.

set -euo pipefail

REPO="${REPO:-oleg-tkachuk/paladin}"
RULESET_NAME="protect-main"
# What the ruleset was called while it also covered `develop`. Looked up so a
# re-run renames and narrows that ruleset in place instead of creating a second
# one beside it that would keep protecting a branch nobody uses.
LEGACY_RULESET_NAME="protect-main-develop"
CHECKS_RULESET_NAME="require-checks-main"
REQUIRE_CHECKS="${REQUIRE_CHECKS:-0}"
# Space-separated refs the required checks apply to.
CHECKS_REFS="${CHECKS_REFS:-refs/heads/main}"

# Job names from .github/workflows/ci.yaml. Each job sets `name:`, so the
# check-run context is that name, not the job id. Keep in sync when a job is
# renamed: a required context that no job reports blocks merges permanently.
#
# A documentation-only change skips these jobs, and GitHub counts a skipped
# required check as passing, so such a pull request is not blocked.
REQUIRED_CONTEXTS=(
  "Verify"           # ci.yaml job `test`
  "Workflow syntax"  # ci.yaml job `syntax`
  "Workflow audit"   # ci.yaml job `audit`
)

command -v gh >/dev/null || { echo "error: gh CLI not found" >&2; exit 1; }
command -v jq >/dev/null || { echo "error: jq not found" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "error: gh is not authenticated (run: gh auth login)" >&2; exit 1; }

visibility=$(gh api "repos/$REPO" --jq .visibility)
echo "Repository: $REPO"
echo "Visibility (left unchanged): $visibility"

echo "==> Disabling auto-delete of head branches on merge"
gh api -X PATCH "repos/$REPO" -F delete_branch_on_merge=false \
  --jq '"    delete_branch_on_merge = " + (.delete_branch_on_merge | tostring)'

echo "==> Enabling private vulnerability reporting"
# SECURITY.md sends reporters to the advisory form; without this the link
# 404s and the documented reporting channel does not exist.
if gh api -X PUT "repos/$REPO/private-vulnerability-reporting" --silent 2>/dev/null; then
  echo "    enabled"
else
  echo "    could not enable (already on, or not available for this repo type)"
fi

echo "==> Enabling Dependabot alerts"
if gh api -X PUT "repos/$REPO/vulnerability-alerts" --silent 2>/dev/null; then
  echo "    enabled"
else
  echo "    could not enable (already on, or insufficient permissions)"
fi

echo "==> Ensuring branch ruleset for main"

# Rules that always apply. To also block force-pushes (history rewrites),
# add { "type": "non_fast_forward" }.
rules=$(jq -n '[{ "type": "deletion" }]')

payload=$(jq -n \
  --arg name "$RULESET_NAME" \
  --argjson rules "$rules" \
  '{
     name: $name,
     target: "branch",
     enforcement: "active",
     conditions: {
       ref_name: {
         include: ["refs/heads/main"],
         exclude: []
       }
     },
     rules: $rules
   }')

# Any further arguments are former names of the same ruleset, tried when no
# ruleset carries the current one.
upsert_ruleset() {
  local name="$1" body="$2" id candidate
  shift 2
  for candidate in "$name" "$@"; do
    id=$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name==\"$candidate\") | .id" 2>/dev/null | head -n1 || true)
    [ -n "$id" ] && break
  done
  if [ -n "$id" ]; then
    echo "    updating existing ruleset #$id"
    printf '%s' "$body" | gh api -X PUT "repos/$REPO/rulesets/$id" --input - \
      --jq '"    " + .name + " -> " + .enforcement'
  else
    echo "    creating ruleset"
    printf '%s' "$body" | gh api -X POST "repos/$REPO/rulesets" --input - \
      --jq '"    " + .name + " -> " + .enforcement'
  fi
}

upsert_ruleset "$RULESET_NAME" "$payload" "$LEGACY_RULESET_NAME"

# Required status checks live in their own ruleset because a ruleset carries a
# single ref condition for every rule it holds, and these apply to a narrower
# set of branches than the deletion rule above.
if [ "$REQUIRE_CHECKS" = "1" ]; then
  echo "==> Ensuring required status checks on: $CHECKS_REFS"
  echo "    contexts: ${REQUIRED_CONTEXTS[*]}"
  # strict_required_status_checks_policy: the branch must be up to date with
  # the base before merging, so a check cannot pass against a stale tree.
  checks_rule=$(printf '%s\n' "${REQUIRED_CONTEXTS[@]}" \
    | jq -R '{ context: . }' \
    | jq -s '{
        type: "required_status_checks",
        parameters: {
          strict_required_status_checks_policy: true,
          required_status_checks: .
        }
      }')
  checks_payload=$(jq -n \
    --arg name "$CHECKS_RULESET_NAME" \
    --argjson rule "$checks_rule" \
    --args '{
       name: $name,
       target: "branch",
       enforcement: "active",
       conditions: { ref_name: { include: $ARGS.positional, exclude: [] } },
       rules: [$rule]
     }' $CHECKS_REFS)
  upsert_ruleset "$CHECKS_RULESET_NAME" "$checks_payload"
else
  echo "==> Required status checks NOT applied (set REQUIRE_CHECKS=1 once CI is green)"
fi

echo "Done."
