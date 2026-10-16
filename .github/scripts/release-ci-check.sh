#!/usr/bin/env bash
# release-ci-check.sh — did CI pass on the commit a release would tag?
#
# Usage: release-ci-check.sh <sha>     (GH_TOKEN and GITHUB_REPOSITORY set)
#
# Prints `release=true` when a push run of ci.yaml on main concluded success
# for that commit, `release=false` otherwise — a line release.yaml appends to
# GITHUB_OUTPUT. A product release is cut on demand (ADR-0034), from main's
# head at the time; this is what keeps that a release of a tested commit, the
# guarantee the CI-dispatched run gets from being dispatched by CI.
#
# An empty sha, or a CI run still in progress, is not a pass.

set -euo pipefail

readonly OUTPUT_KEY=release
readonly CI_WORKFLOW=ci.yaml
readonly RELEASE_BRANCH=main
readonly PASSED=success

sha="${1:-}"
if [ -z "$sha" ]; then
    echo "!!! no sha: cannot tell which commit to check" >&2
    exit 1
fi

conclusions=$(gh api \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${CI_WORKFLOW}/runs?head_sha=${sha}&event=push&branch=${RELEASE_BRANCH}" \
    --jq '.workflow_runs[].conclusion')

if printf '%s\n' "$conclusions" | grep -qx "$PASSED"; then
    echo "${OUTPUT_KEY}=true"
else
    echo "CI has not passed on $sha (runs: ${conclusions:-none}); release it once it has" >&2
    echo "${OUTPUT_KEY}=false"
fi
