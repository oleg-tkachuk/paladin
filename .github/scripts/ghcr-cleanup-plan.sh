#!/usr/bin/env bash
# ghcr-cleanup-plan.sh — what a dev publish removes from GHCR afterwards.
#
# Usage: ghcr-cleanup-plan.sh <image package> <chart package>
#
# Every green merge to main publishes a dev build: an image and a chart, each
# with its platform manifests, signatures and SBOM attestations. Nothing
# removed them, and GitHub has no retention setting for its container
# registry. So the publish that adds a dev build deletes the older ones,
# keeping only the newest — the one the dev cluster runs. Releases are never
# matched: their versions carry no dev mark.
#
# Printed as step outputs for dataaxiom/ghcr-cleanup-action, which deletes a
# version with its children and keeps those of what it keeps.

set -euo pipefail

# shellcheck source=SCRIPTDIR/dev-mark.sh
. "$(dirname "$0")/dev-mark.sh"

# Dev builds kept per package: the newest, which the dev cluster follows.
readonly DEV_BUILDS_KEPT=1
# Deletes for real: the first dry run's list was read against the registry —
# only dev builds and their children, no release, the newest kept.
readonly DRY_RUN=false

image="${1:-}"
chart="${2:-}"
[ -n "$image" ] && [ -n "$chart" ] || {
    printf '!!! usage: %s <image package> <chart package>\n' "$0" >&2
    exit 1
}

printf 'packages=%s,%s\n' "$image" "$chart"
printf 'delete_tags=*%s*\n' "$DEV_MARK"
printf 'keep=%s\n' "$DEV_BUILDS_KEPT"
printf 'dry_run=%s\n' "$DRY_RUN"
