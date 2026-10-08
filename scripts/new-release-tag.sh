#!/usr/bin/env bash
# new-release-tag.sh — the release tag this run pushed, if any.
#
# Usage: new-release-tag.sh <file listing the tags on HEAD before semantic-release> [streams]
#
# Prints the v* tag on HEAD that is not in that list, or nothing. With
# `streams`, prints instead every new SDK and capability tag (sdk/go/v*,
# capability/v*), one per line — the module streams cut in the same run. "A v* tag on
# HEAD" alone is not the same question: release.yaml is dispatched once per CI
# run on main and always checks out the branch head, so two dispatches can land
# on one commit. The second found the first run's tag on HEAD, took it for its
# own, and failed creating a release that already existed.
#
# Only v* tags: the api/vX.Y.Z proto baselines can point at the same commit and
# are not releases.
#
# A tag already on HEAD whose GitHub release does not exist is reported too,
# when no tag is new: a run that pushed the tag and failed before publishing
# it. semantic-release does not tag twice, so re-running that run finds no new
# tag; this is how the re-run publishes what the first one left behind. A tag
# with its release is a finished release and stays out, so a second dispatch
# to the same commit still publishes nothing.

set -euo pipefail

readonly RELEASE_TAG='^v[0-9]'
readonly STREAM_TAG='^(sdk/go|capability)/v[0-9]'
readonly STREAMS_MODE=streams

before="${1:?usage: new-release-tag.sh <tags-before-file> [streams]}"

new() { git tag --points-at HEAD | grep -E "$1" | grep -vxF -f "$before" || true; }

# unpublished prints the tags on HEAD matching $1 that have no GitHub release.
unpublished() {
    local tag
    git tag --points-at HEAD | grep -E "$1" | while read -r tag; do
        gh release view "$tag" >/dev/null 2>&1 || echo "$tag"
    done
}

# The new tags matching $1, or, when there are none, the unpublished ones.
pending() {
    local tags
    tags="$(new "$1")"
    [ -n "$tags" ] || tags="$(unpublished "$1")"
    [ -z "$tags" ] || printf '%s\n' "$tags"
}

if [ "${2:-}" = "$STREAMS_MODE" ]; then
    pending "$STREAM_TAG"
else
    pending "$RELEASE_TAG" | head -1
fi
