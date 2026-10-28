#!/usr/bin/env bash
# coverage-profile.sh — one coverage profile of a Go module's tests, each
# block once, counted across every test package that reached it.
#
#   scripts/coverage-profile.sh -coverprofile=<path> [go test arguments...]
#
# crap4go's {coverprofile} placeholder expands to the first argument. A plain
# `go test -coverpkg=... -coverprofile=...` over many packages writes every
# block once per test package, and crap4go sums those copies, so a function
# one package of a hundred exercises reads as 1% covered. The raw data go
# test writes to a directory is merged by `go tool covdata` instead.
#
# Run from the module's directory. The go test arguments default to ./...

set -euo pipefail

readonly PROFILE_FLAG=-coverprofile=
readonly EXIT_USAGE=2

if [ $# -lt 1 ] || [ "${1#"$PROFILE_FLAG"}" = "$1" ]; then
    echo "usage: $0 ${PROFILE_FLAG}<path> [go test arguments...]" >&2
    exit "$EXIT_USAGE"
fi
profile=${1#"$PROFILE_FLAG"}
shift
[ $# -gt 0 ] || set -- ./...

raw=$(mktemp -d)
trap 'rm -rf "$raw"' EXIT
mkdir -p "$(dirname "$profile")"

go test -cover "$@" -args -test.gocoverdir="$raw"
go tool covdata textfmt -i="$raw" -o="$profile"
