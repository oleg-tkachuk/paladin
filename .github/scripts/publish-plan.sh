#!/usr/bin/env bash
# publish-plan.sh — what this release run publishes.
#
# Usage: publish-plan.sh <product-tag> <event> <dispatched-sha>
#
# Prints the lines release.yaml appends to GITHUB_OUTPUT:
#
#   kind=release ref=<tag>   a product tag was cut: publish the release at it
#   kind=dev     ref=<sha>   no tag, and CI dispatched this run for <sha>:
#                            publish a dev build of it, for the dev cluster
#   (nothing)                a manual run that cut no tag, or CI named no
#                            commit: publish nothing
#
# A dev build is every commit that passed CI on main, published so the dev
# cluster runs each change as it lands. CI dispatches a run only for a commit
# it passed, so the dispatch is the proof; the commit is built from its own
# sha, so it does not matter that main has moved on. It once did: the run
# built only a head that was still the dispatched commit, and a merge landing
# while that CI finished left the commit unpublished. The next head was often
# documentation, which dispatches nothing, so the dev cluster stayed behind
# two merged code changes.

set -euo pipefail

readonly KIND_RELEASE=release
readonly KIND_DEV=dev
# The event CI's dispatch-release job starts this run with.
readonly EVENT_CI_DISPATCH=repository_dispatch

tag="${1:-}"
event="${2:-}"
sha="${3:-}"

if [ -n "$tag" ]; then
    printf 'kind=%s\nref=%s\n' "$KIND_RELEASE" "$tag"
elif [ "$event" = "$EVENT_CI_DISPATCH" ] && [ -n "$sha" ]; then
    printf 'kind=%s\nref=%s\n' "$KIND_DEV" "$sha"
else
    echo "nothing to publish: no tag, and CI dispatched no commit" >&2
fi
