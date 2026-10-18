#!/usr/bin/env bash
# release-retry.sh — sourced by the release scripts. Defines `retry`, which
# runs a command again when it fails, waiting longer each time.
#
#   retry <command> [args...]
#
# For the calls that reach a public service a release does not control: the
# Sigstore transparency log answered one signing with a 409 conflict, and the
# image it was signing never reached the dev cluster. A command that fails
# every attempt still fails the release, with its own exit status.
#
# RETRY_ATTEMPTS and RETRY_DELAY_SECONDS override the defaults; the test sets
# the delay to zero.

# How many times a command runs before its failure stands.
readonly RETRY_DEFAULT_ATTEMPTS=4
# The wait before the second attempt; it doubles before each one after.
readonly RETRY_DEFAULT_DELAY_SECONDS=10

retry() {
    local attempts=${RETRY_ATTEMPTS:-$RETRY_DEFAULT_ATTEMPTS}
    local delay=${RETRY_DELAY_SECONDS:-$RETRY_DEFAULT_DELAY_SECONDS}
    local attempt=1 status
    while true; do
        "$@" && return 0
        status=$?
        if [ "$attempt" -ge "$attempts" ]; then
            echo "!!! [retry] gave up after $attempt attempts: $1" >&2
            return "$status"
        fi
        echo "!!! [retry] attempt $attempt of $attempts failed ($status): $1; again in ${delay}s" >&2
        sleep "$delay"
        attempt=$((attempt + 1))
        delay=$((delay * 2))
    done
}
