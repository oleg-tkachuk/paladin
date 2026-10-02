#!/usr/bin/env bash
# release-sign-chart.sh — sign a Helm chart pushed to an OCI registry, and
# verify the signature before the release goes out.
#
#   scripts/release-sign-chart.sh <chart-reference>    e.g. ghcr.io/o/charts/c:1.2.3
#
# The chart is signed by digest, as the images are, and with the same identity:
# a tag can be moved after it is signed, a digest cannot. The digest comes from
# crane rather than from `helm push`, which reports it only as a line of text.
#
# Signing mode (keyless, or a key pair for tests): see release-sign-mode.sh.
# Requires: crane, cosign — both on PATH.

set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "usage: $0 <chart-reference>" >&2
    exit 2
fi
chart=$1

# shellcheck source=SCRIPTDIR/release-sign-mode.sh
source "$(dirname "$0")/release-sign-mode.sh"

digest=$(crane digest "$chart")
ref="${chart%:*}@${digest}"

echo ">>> [sign] $chart ($digest)"
cosign sign --yes "${sign_args[@]}" "$ref"
cosign verify "${verify_args[@]}" "$ref" >/dev/null
echo "    signed and verified"
