#!/usr/bin/env bash
#
# Mint an HS256 JWT for local development.
#
# Lives outside Taskfile.yaml because 35 lines of bash inside YAML is a
# maintenance trap — single quotes inside printf inside `cmd: |-` quickly
# becomes unreadable. The task wrapper just exports defaults and execs
# this script.
#
# All inputs are environment variables; tweak only what you need:
#
#   JWT_SECRET       HS256 signing key (must be ≥ 32 bytes for prod-style
#                    parity with the issuer's CUE constraint).
#   JWT_ISS          `iss` claim. Defaults to "paladin-dev".
#   JWT_AUD          `aud` claim. Plane-specific in v2 deploys
#                    (paladin-data | paladin-admin | paladin-iam); defaults to a
#                    cross-plane "paladin-api" for ad-hoc dev.
#   JWT_SUB          `sub` claim.
#   JWT_ROLES        Comma-separated role list. platform.admin (dot form;
#                    NOT hyphen) is required for cross-tenant admin RPCs.
#   JWT_TENANT       Tenant UUID. Required by per-tenant RPCs. Empty
#                    means "platform-only token" (cross-tenant admin).
#   JWT_TENANT_SLUG  Tenant slug. Optional; populated as `tenant_slug`
#                    claim. Cedar prefers slug-keyed Tenant UIDs when set.
#   JWT_TTL_SECONDS  Token lifetime. Default 70 days for dev convenience.

set -euo pipefail

: "${JWT_SECRET:?JWT_SECRET must be set}"
: "${JWT_ISS:=paladin-dev}"
: "${JWT_AUD:=paladin-api}"
: "${JWT_SUB:=dev-user}"
: "${JWT_ROLES:=platform.admin}"
: "${JWT_TENANT:=}"
: "${JWT_TENANT_SLUG:=}"
: "${JWT_TTL_SECONDS:=6048000}"  # 70 days

now=$(date +%s)
exp=$((now + JWT_TTL_SECONDS))

# Comma-separated → JSON array. awk handles the empty-list edge case
# cleanly (printf "[]" when NF==0).
roles_json=$(printf '%s' "$JWT_ROLES" | awk -F, '{
    printf "[";
    for (i = 1; i <= NF; i++) {
        if (i > 1) printf ",";
        printf "\"%s\"", $i;
    }
    printf "]";
}')

# Optional claims appended only when set, so the resulting payload stays
# minimal — easier to eyeball when debugging.
optional_claims=""
[[ -n "$JWT_TENANT"      ]] && optional_claims+=$(printf ',"tenant":"%s"' "$JWT_TENANT")
[[ -n "$JWT_TENANT_SLUG" ]] && optional_claims+=$(printf ',"tenant_slug":"%s"' "$JWT_TENANT_SLUG")

b64() { openssl base64 -e -A | tr '+/' '-_' | tr -d '='; }

header=$(printf '%s' '{"alg":"HS256","typ":"JWT"}' | b64)
payload=$(printf \
    '{"iss":"%s","aud":"%s","sub":"%s","iat":%d,"nbf":%d,"exp":%d,"roles":%s%s}' \
    "$JWT_ISS" "$JWT_AUD" "$JWT_SUB" "$now" "$now" "$exp" "$roles_json" "$optional_claims" \
    | b64)
sig=$(printf '%s' "${header}.${payload}" | openssl dgst -binary -sha256 -hmac "$JWT_SECRET" | b64)

printf '%s.%s.%s\n' "$header" "$payload" "$sig"
