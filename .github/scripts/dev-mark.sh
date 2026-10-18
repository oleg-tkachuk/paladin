# dev-mark.sh — what marks a dev build's version, <x.y.z>-dev.<…>. Sourced by
# every script that tells a dev build from a release: the check that refuses
# to publish one as the other, and the cleanup that deletes only dev builds.
# shellcheck shell=bash
readonly DEV_MARK="-dev."
