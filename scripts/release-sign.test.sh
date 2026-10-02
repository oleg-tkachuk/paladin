#!/usr/bin/env bash
# release-sign.test.sh — scripts/release-sign.sh against a throwaway registry.
#
# Copies a published multi-arch Paladin image — with BuildKit's attestation
# manifests in its index, like every image release.yaml pushes — into a local
# registry, signs it with a fresh key pair, and checks what came out:
#
#   - an SPDX SBOM per platform image, and none for the attestation manifests;
#   - signatures and SBOM attestations that verify with the signing key;
#   - and do NOT verify with another key, so the check above is not vacuous;
#   - the same for a Helm chart pushed to the registry, through
#     release-sign-chart.sh.
#
# Keyless signing needs a CI OIDC token, so release.yaml's own verify step is
# the only place that path runs; everything else is the same code.
#
# Runs as `task -t Taskfile.dev.yaml verify:release-signing`.
# Requires: docker (buildx), jq, cosign, syft, crane, and network to ghcr.io.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/scripts/release-sign.sh"
chart_script="$root/scripts/release-sign-chart.sh"

# A released image: multi-arch, with attestation manifests. Pinned by tag; the
# test reads its structure, not its contents.
readonly SOURCE_IMAGE=ghcr.io/oleg-tkachuk/paladin-core:4.11.5
readonly PLATFORMS_WANT="linux-amd64 linux-arm64"
readonly REGISTRY_IMAGE=registry:3
readonly REGISTRY_PORT=${PALADIN_SIGN_TEST_PORT:-5055}
readonly TEST_REPO=paladin-sign-test
readonly SPDX_PREFIX=SPDX-
# A chart of helm's own scaffold, pushed under the path the release library
# uses; the test reads its signature, not its contents.
readonly TEST_CHART=paladin-sign-test-chart
readonly TEST_CHART_VERSION=0.0.1
readonly CHART_PATH=charts

for tool in docker jq cosign syft crane helm; do
    command -v "$tool" >/dev/null 2>&1 || { echo "!!! $tool is not installed" >&2; exit 1; }
done

work=$(mktemp -d "${TMPDIR:-/tmp}/release-sign-test.XXXXXX")
registry=""
cleanup() {
    [ -n "$registry" ] && docker rm -f "$registry" >/dev/null 2>&1 || true
    rm -rf "$work"
}
trap cleanup EXIT

registry=$(docker run -d --rm -p "127.0.0.1:${REGISTRY_PORT}:5000" "$REGISTRY_IMAGE")
image="localhost:${REGISTRY_PORT}/${TEST_REPO}"
for _ in $(seq 1 30); do
    crane catalog "localhost:${REGISTRY_PORT}" >/dev/null 2>&1 && break
    sleep 1
done
crane copy "$SOURCE_IMAGE" "${image}:test" >/dev/null 2>&1
digest=$(crane digest "${image}:test")

(
    cd "$work"
    COSIGN_PASSWORD="" cosign generate-key-pair --output-key-prefix signer >/dev/null 2>&1
    COSIGN_PASSWORD="" cosign generate-key-pair --output-key-prefix other >/dev/null 2>&1
)

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

COSIGN_PASSWORD="" COSIGN_KEY="$work/signer.key" COSIGN_PUB="$work/signer.pub" \
    "$script" "$image" "$digest" "$work/sbom" || fail "release-sign.sh exited non-zero"

got=$(find "$work/sbom" -name '*.spdx.json' -exec basename {} \; 2>/dev/null |
    sed -E "s/^${TEST_REPO}-(.*)\.spdx\.json$/\1/" | sort | xargs)
[ "$got" = "$PLATFORMS_WANT" ] || fail "SBOMs for [$got], want [$PLATFORMS_WANT]"
for f in "$work"/sbom/*.spdx.json; do
    [ -e "$f" ] || continue
    jq -e --arg p "$SPDX_PREFIX" '.spdxVersion | startswith($p)' "$f" >/dev/null ||
        fail "$(basename "$f") is not an SPDX document"
done

# The other key must not verify anything this run signed.
if cosign verify --key "$work/other.pub" --insecure-ignore-tlog=true "${image}@${digest}" >/dev/null 2>&1; then
    fail "the index verifies with a key that did not sign it"
fi
pdigest=$(crane manifest "${image}@${digest}" | jq -r '.manifests[] | select(.platform.os == "linux") | .digest' | head -1)
if cosign verify-attestation --key "$work/other.pub" --insecure-ignore-tlog=true --type spdxjson \
    "${image}@${pdigest}" >/dev/null 2>&1; then
    fail "an SBOM attestation verifies with a key that did not sign it"
fi

(
    cd "$work"
    helm create "$TEST_CHART" >/dev/null
    helm package "$TEST_CHART" --version "$TEST_CHART_VERSION" >/dev/null
    helm push "${TEST_CHART}-${TEST_CHART_VERSION}.tgz" \
        "oci://localhost:${REGISTRY_PORT}/${CHART_PATH}" --plain-http >/dev/null 2>&1
)
chart="localhost:${REGISTRY_PORT}/${CHART_PATH}/${TEST_CHART}:${TEST_CHART_VERSION}"
COSIGN_PASSWORD="" COSIGN_KEY="$work/signer.key" COSIGN_PUB="$work/signer.pub" \
    "$chart_script" "$chart" || fail "release-sign-chart.sh exited non-zero"
chart_ref="${chart%:*}@$(crane digest "$chart")"
cosign verify --key "$work/signer.pub" --insecure-ignore-tlog=true "$chart_ref" >/dev/null 2>&1 ||
    fail "the chart does not verify with its signing key"
if cosign verify --key "$work/other.pub" --insecure-ignore-tlog=true "$chart_ref" >/dev/null 2>&1; then
    fail "the chart verifies with a key that did not sign it"
fi

if [ "$failed" -ne 0 ]; then
    exit 1
fi
printf '%s\n' "release signing: image and chart signed, SBOM per platform, verify with their key only"
