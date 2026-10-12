#!/usr/bin/env bash
# release-sign-mode.sh — sourced by release-sign.sh and release-sign-chart.sh.
# Sets sign_args and verify_args for cosign from the environment:
#
#   keyless (default) — Sigstore with the job's OIDC token; verification pins
#     CERT_IDENTITY (the signing workflow) and CERT_OIDC_ISSUER.
#   COSIGN_KEY / COSIGN_PUB set — a key pair and no transparency log; what
#     release-sign.test.sh uses against a throwaway registry.

# shellcheck disable=SC2034 # read by the scripts that source this file
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
