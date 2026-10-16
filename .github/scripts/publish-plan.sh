#!/usr/bin/env bash
# publish-plan.sh — what this release run publishes.
#
# Usage: publish-plan.sh <product-tag> <head-is-tested> <tested-sha>
#
# Prints the lines release.yaml appends to GITHUB_OUTPUT:
#
#   kind=release ref=<tag>   a product tag was cut: publish the release at it
#   kind=dev     ref=<sha>   no tag, but the head is the commit CI passed:
#                            publish a dev build of it, for the dev cluster
#   (nothing)                main moved past the tested commit, or CI named
#                            none: publish nothing
#
# A dev build is every commit that passed CI on main, published so the dev
# cluster runs each change as it lands; a release is the tag a release run
# cuts. Only the tested commit is built: a newer head is built by its own run.

set -euo pipefail

readonly KIND_RELEASE=release
readonly KIND_DEV=dev
readonly TESTED=true

tag="${1:-}"
tested="${2:-}"
sha="${3:-}"

if [ -n "$tag" ]; then
    printf 'kind=%s\nref=%s\n' "$KIND_RELEASE" "$tag"
elif [ "$tested" = "$TESTED" ] && [ -n "$sha" ]; then
    printf 'kind=%s\nref=%s\n' "$KIND_DEV" "$sha"
else
    echo "nothing to publish: no tag, and the head is not the tested commit" >&2
fi
