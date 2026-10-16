#!/usr/bin/env bash
# publish-version-check.sh — is the derived version the one this kind of
# publish may push?
#
# Usage: publish-version-check.sh <kind> <derived-version> [<tag>]
#
#   release  the version is the tag's, without its leading v
#   dev      the version is a prerelease of the next patch, <x.y.z>-dev.<…>,
#            which sorts after the last release and before the next
#
# A dev publish that derived a release version — a run that raced a tag —
# would push a release without the release run that announces it, so it is
# refused rather than published.

set -euo pipefail

readonly DEV_MARK="-dev."

kind="${1:-}"
version="${2:-}"
tag="${3:-}"

refuse() {
    printf '!!! %s\n' "$1" >&2
    exit 1
}

[ -n "$version" ] || refuse "no version derived"
case "$kind" in
    release)
        [ "$version" = "${tag#v}" ] || refuse "the library derives $version, the tag is $tag — refusing to publish"
        ;;
    dev)
        case "$version" in
            *"$DEV_MARK"*) ;;
            *) refuse "a dev build derived $version, a release version — refusing to publish it" ;;
        esac
        ;;
    *)
        refuse "unknown publish kind: ${kind:-<none>}"
        ;;
esac
