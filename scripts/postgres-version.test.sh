#!/usr/bin/env bash
# postgres-version.test.sh — every Postgres the repository starts is one
# version: the compose stacks' server and the testcontainers suites' image.
#
# They had drifted to three — 16 in the compose stacks, 16 and 17 across the
# Go suites — so a test could pass against a server no deployment ran. Renovate
# moves them as one group (renovate.json); this fails if a pin is left behind.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly COMPOSE_FILES=(backend/deploy/docker-compose.yaml frontend/tests/e2e/docker-compose.test.yaml)
readonly GO_IMAGE_FILE=backend/internal/pgtest/image.go
readonly IMAGE=postgres

# version <image ref>: the server version a ref names. The variant suffix
# ("-alpine") and a pinned digest ("@sha256:…") name the same server.
version() {
    local ref=${1#"$IMAGE":}
    ref=${ref%%@*}
    echo "${ref%%-*}"
}

for case in "postgres:18.6=18.6" "postgres:18.6-alpine=18.6" "postgres:18@sha256:abc=18" "postgres:18.6-alpine@sha256:abc=18.6"; do
    got=$(version "${case%=*}")
    [ "$got" = "${case#*=}" ] || { echo "FAIL version ${case%=*} = $got, want ${case#*=}" >&2; exit 1; }
done

cd "$root"
refs=()
for f in "${COMPOSE_FILES[@]}"; do
    while IFS= read -r ref; do refs+=("$f $ref"); done < <(
        yq --yaml-fix-merge-anchor-to-spec=true -e ".services[].image | select(test(\"^${IMAGE}:\"))" "$f")
done
go_ref=$(sed -n "s/^const Image = \"\(${IMAGE}:[^\"]*\)\"$/\1/p" "$GO_IMAGE_FILE")
[ -n "$go_ref" ] || { echo "FAIL no Postgres image constant in $GO_IMAGE_FILE" >&2; exit 1; }
refs+=("$GO_IMAGE_FILE $go_ref")

want=$(version "${refs[0]#* }")
failed=0
for r in "${refs[@]}"; do
    got=$(version "${r#* }")
    if [ "$got" != "$want" ]; then
        echo "FAIL ${r%% *} runs Postgres $got, ${refs[0]%% *} runs $want" >&2
        failed=1
    fi
done
[ "$failed" -eq 0 ] || exit 1
echo "ok   every Postgres the repository starts is $want (${#refs[@]} pins)"
