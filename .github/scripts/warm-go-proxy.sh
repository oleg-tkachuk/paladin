#!/usr/bin/env bash
# warm-go-proxy.sh — tell the Go module proxy about a module tag just pushed.
#
# Usage: warm-go-proxy.sh <tag>        e.g. sdk/go/v0.16.0
#
# proxy.golang.org learns of a version when someone asks for it, and until
# then its @latest — which `go get` and the README's SDK badge read — keeps
# answering the previous one. Asking for the version's .info, the endpoint
# of the GOPROXY protocol `go list -m module@version` would ask, makes it
# fetch the tag now. The module is this repository's path plus the tag's
# directory prefix.

set -euo pipefail

tag="${1:?usage: warm-go-proxy.sh <tag>}"
repo="${GITHUB_REPOSITORY:-oleg-tkachuk/paladin}"
proxy="${GOPROXY_URL:-https://proxy.golang.org}"
# The proxy fetches from GitHub on the first request; give it a few tries.
readonly ATTEMPTS=5
readonly PAUSE_SECONDS=10

version="${tag##*/}"
directory="${tag%/"$version"}"
if [[ "$directory" == "$tag" || ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "!!! $tag is not a module tag (<dir>/vX.Y.Z)" >&2
    exit 1
fi
module="github.com/${repo}/${directory}"
# The protocol escapes upper-case letters in a module path; this repository's
# paths are lower-case, which the check below keeps true.
if [[ "$module" =~ [A-Z] ]]; then
    echo "!!! $module has upper-case letters, which the proxy protocol escapes" >&2
    exit 1
fi

curl -fsS --retry "$ATTEMPTS" --retry-delay "$PAUSE_SECONDS" --retry-all-errors \
    "${proxy}/${module}/@v/${version}.info"
echo
