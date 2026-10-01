#!/usr/bin/env bash
# new-release-tag.sh — the release tag this run pushed, if any.
#
# Usage: new-release-tag.sh <file listing the tags on HEAD before semantic-release>
#
# Prints the v* tag on HEAD that is not in that list, or nothing. "A v* tag on
# HEAD" alone is not the same question: release.yaml is dispatched once per CI
# run on main and always checks out the branch head, so two dispatches can land
# on one commit. The second found the first run's tag on HEAD, took it for its
# own, and failed creating a release that already existed.
#
# Only v* tags: the api/vX.Y.Z proto baselines can point at the same commit and
# are not releases.

set -euo pipefail

readonly RELEASE_TAG='^v[0-9]'

before="${1:?usage: new-release-tag.sh <tags-before-file>}"

git tag --points-at HEAD | grep -E "$RELEASE_TAG" | grep -vxF -f "$before" | head -1 || true
