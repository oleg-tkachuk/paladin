#!/usr/bin/env bash
# go-sdk-consumer.sh — build a fresh consumer of the Go SDK the way a consumer
# resolves it: `go get` of a commit, no `replace`, no workspace.
#
# The backend builds the SDK through `replace => ../sdk/go`, so its module
# graph is the backend's, not one a consumer gets from sdk/go/go.mod alone. A
# requirement only a consumer sees — a version no proxy serves, a `replace`
# inside sdk/go/go.mod that consumers silently ignore, a file the module zip
# leaves out — passes there and fails here.
#
# The commit is fetched from the local clone (git's url.insteadOf, set through
# the environment only), so any committed tree can be checked, pushed or not.
# Every other module comes through the proxy and the checksum database, as it
# does for a consumer.
#
# Usage: go-sdk-consumer.sh [repo-dir] [commit]   (default: this repo, HEAD)
# Runs from `task -t Taskfile.dev.yaml verify-go-sdk`. Requires network.

set -euo pipefail

repo=$(cd "${1:-$(git rev-parse --show-toplevel)}" && pwd)
ref=${2:-HEAD}
readonly REPO_URL=https://github.com/oleg-tkachuk/paladin
readonly REPO_MODULE=github.com/oleg-tkachuk/paladin
readonly SDK_MODULE=$REPO_MODULE/sdk/go
readonly SDK_PACKAGE=$SDK_MODULE/paladin
readonly LIMES_MODULE=github.com/oleg-tkachuk/limes
readonly CONSUMER_MODULE=example.com/paladin-sdk-consumer

log() { printf 'go sdk consumer: %s\n' "$1"; }

if ! git -C "$repo" diff --quiet HEAD -- sdk/go; then
    log "WARN sdk/go has uncommitted changes; checking the commit, which does not hold them"
fi
sha=$(git -C "$repo" rev-parse --verify "${ref}^{commit}")
log "resolving $SDK_MODULE at $sha from $repo"

work=$(mktemp -d "${TMPDIR:-/tmp}/go-sdk-consumer.XXXXXX")
trap 'rm -rf "$work"' EXIT

# The paladin repository from the local clone, everything else as usual.
# GOPRIVATE makes go fetch the SDK directly and skip the checksum database,
# which cannot know an unpublished commit; it applies to nothing else.
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="url.file://$repo.insteadOf"
export GIT_CONFIG_VALUE_0=$REPO_URL
export GOPRIVATE=$REPO_MODULE
export GOWORK=off
export GOFLAGS=-mod=mod

cd "$work"
go mod init "$CONSUMER_MODULE" >/dev/null 2>&1
# The smallest real consumer: build a client with a credential.
cat >main.go <<EOF
package main

import (
	"fmt"

	"$SDK_PACKAGE"
)

func main() {
	client, err := paladin.Connect(paladin.Endpoints{Data: "http://localhost"}, paladin.WithBearerToken("token"))
	fmt.Println(client != nil, err)
}
EOF

if ! go get "$SDK_PACKAGE@$sha" >"$work/get.log" 2>&1 || ! go mod tidy >>"$work/get.log" 2>&1; then
    log "ERROR a consumer cannot resolve $SDK_MODULE at $sha:"
    cat "$work/get.log" >&2
    exit 1
fi
if ! go build -o /dev/null . 2>"$work/build.log"; then
    log "ERROR a consumer resolves $SDK_MODULE at $sha but cannot build against it:"
    cat "$work/build.log" >&2
    exit 1
fi

log "built against $(go list -m "$SDK_MODULE"), selecting $(go list -m "$LIMES_MODULE")"
