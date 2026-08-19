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
#   2. Turns OFF "automatically delete head branches" on merge, so a
#      develop -> main PR merge never auto-removes develop.
#   3. Enables private vulnerability reporting and Dependabot alerts. Both
#      are free on public repositories, and SECURITY.md points contributors
#      at the private-reporting form — a form that 404s if this is not on.
#   4. Creates/updates a branch RULESET protecting main and develop.
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
# Verify a run has actually gone green on each context first — the names
# below are the check-run names GitHub reports, which are the JOB ids in
# the workflow files, not the workflow names.

set -euo pipefail

REPO="${REPO:-oleg-tkachuk/paladin-private}"
RULESET_NAME="protect-main-develop"
REQUIRE_CHECKS="${REQUIRE_CHECKS:-0}"

# Job ids from .github/workflows/*. None of these jobs sets `name:`, so the
# check-run context equals the job id. Keep in sync when a job is renamed:
# a required context that no job reports blocks merges permanently.
#
# The slower jobs — `postgres-backed` (integration.yml), `e2e` (e2e.yml),
# `standalone` (capability-module.yml) — are deliberately not required yet.
# Add them once their runtimes on public runners are known; a required
# check that routinely times out is worse than one that is merely advisory.
REQUIRED_CONTEXTS=(
  "backend"    # test.yml
  "frontend"   # test.yml
  "gitleaks"   # security.yml
  "trivy-fs"   # security.yml
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

echo "==> Ensuring branch ruleset for main + develop"

# Rules that always apply. To also block force-pushes (history rewrites),
# add { "type": "non_fast_forward" }.
rules=$(jq -n '[{ "type": "deletion" }]')

if [ "$REQUIRE_CHECKS" = "1" ]; then
  echo "    including required status checks: ${REQUIRED_CONTEXTS[*]}"
  # strict_required_status_checks_policy: a PR must be up to date with the
  # base before merging, so a check cannot pass against a stale tree.
  checks=$(printf '%s\n' "${REQUIRED_CONTEXTS[@]}" \
    | jq -R '{ context: . }' \
    | jq -s '{
        type: "required_status_checks",
        parameters: {
          strict_required_status_checks_policy: true,
          required_status_checks: .
        }
      }')
  rules=$(jq -n --argjson base "$rules" --argjson add "$checks" '$base + [$add]')
else
  echo "    required status checks NOT applied (set REQUIRE_CHECKS=1 once CI is green)"
fi

payload=$(jq -n \
  --arg name "$RULESET_NAME" \
  --argjson rules "$rules" \
  '{
     name: $name,
     target: "branch",
     enforcement: "active",
     conditions: {
       ref_name: {
         include: ["refs/heads/main", "refs/heads/develop"],
         exclude: []
       }
     },
     rules: $rules
   }')

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
