#!/usr/bin/env bash
# release-sign.sh — sign a published image, attest an SBOM for each of its
# platforms, and verify both before the release goes out.
#
#   scripts/release-sign.sh <image> <index-digest> <sbom-dir>
#
# The image is addressed by digest throughout: a tag can be moved after it is
# signed, a digest cannot.
#
#   - The index and every platform manifest are signed (cosign --recursive), so
#     a pull by tag and a pull by platform digest both find a signature.
#   - Each platform gets its own SPDX SBOM. Syft run against an index scans one
#     platform only, so an SBOM attached to the index would describe an image
#     half the pullers never get. The SBOM is attested (signed in-toto) to that
#     platform's digest and written to <sbom-dir> for the release assets.
#   - Both are verified right after, against the identity they must carry. A
#     signature that does not verify fails the release here, not in a user's
#     admission controller.
#
# Signing mode, from the environment:
#   keyless (default) — Sigstore with the job's OIDC token; verification pins
#     CERT_IDENTITY (the signing workflow) and CERT_OIDC_ISSUER.
#   COSIGN_KEY / COSIGN_PUB set — a key pair and no transparency log; what
#     release-sign.test.sh uses against a throwaway registry.
#
# Requires: docker (buildx), jq, cosign, syft — all on PATH.

set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "usage: $0 <image> <index-digest> <sbom-dir>" >&2
    exit 2
fi
image=$1
digest=$2
sbom_dir=$3

# The predicate type cosign names an SPDX JSON document by; verification asks
# for the same one.
readonly SBOM_TYPE=spdxjson
readonly SBOM_FORMAT=spdx-json
# BuildKit stores its own attestations as manifests with this platform; they
# are not images and have nothing to scan.
readonly ATTESTATION_OS=unknown

if [ -n "${COSIGN_KEY:-}" ]; then
    : "${COSIGN_PUB:?COSIGN_PUB must be set with COSIGN_KEY}"
    sign_args=(--key "$COSIGN_KEY" --use-signing-config=false --tlog-upload=false)
    verify_args=(--key "$COSIGN_PUB" --insecure-ignore-tlog=true)
else
    : "${CERT_IDENTITY:?CERT_IDENTITY must name the signing workflow}"
    : "${CERT_OIDC_ISSUER:?CERT_OIDC_ISSUER must name the token issuer}"
    sign_args=()
    verify_args=(--certificate-identity "$CERT_IDENTITY"
        --certificate-oidc-issuer "$CERT_OIDC_ISSUER")
fi

mkdir -p "$sbom_dir"
ref="${image}@${digest}"

# "<os>-<arch>[-<variant>] <digest>" per platform image in the index.
platforms="$(docker buildx imagetools inspect "$ref" --format '{{json .Manifest}}' |
    jq -r --arg skip "$ATTESTATION_OS" '
        .manifests[]
        | select(.platform.os != $skip)
        | "\(.platform.os)-\(.platform.architecture)\(if .platform.variant then "-" + .platform.variant else "" end) \(.digest)"')"
if [ -z "$platforms" ]; then
    echo "!!! $ref lists no platform images — refusing to sign an empty index" >&2
    exit 1
fi

echo ">>> [sign] $ref and its platform manifests"
cosign sign --yes --recursive "${sign_args[@]}" "$ref"

name="${image##*/}"
while read -r platform pdigest; do
    pref="${image}@${pdigest}"
    sbom="${sbom_dir}/${name}-${platform}.spdx.json"
    echo ">>> [sbom] $platform: $pref"
    syft scan "registry:${pref}" --output "${SBOM_FORMAT}=${sbom}" --quiet
    cosign attest --yes "${sign_args[@]}" --type "$SBOM_TYPE" --predicate "$sbom" "$pref"
done <<<"$platforms"

echo ">>> [verify] signatures and SBOM attestations"
cosign verify "${verify_args[@]}" "$ref" >/dev/null
while read -r platform pdigest; do
    cosign verify "${verify_args[@]}" "${image}@${pdigest}" >/dev/null
    cosign verify-attestation "${verify_args[@]}" --type "$SBOM_TYPE" "${image}@${pdigest}" >/dev/null
    echo "    $platform: signed, SBOM attested"
done <<<"$platforms"
