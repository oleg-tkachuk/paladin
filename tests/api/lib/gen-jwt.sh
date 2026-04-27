#!/usr/bin/env bash
# Mints an HS256 JWT for the dev backend.
#
# The PALADIN dev config (configs/config.yaml) verifies tokens with
#   issuer:  paladin-dev
#   aud:     paladin-api
#   secret:  dev-secret-change-me-32-bytes-min
#
# Usage
#   tests/api/lib/gen-jwt.sh <tenant_uuid> [subject] [role,role,...]
#
# Defaults
#   subject = e2e-runner
#   roles   = platform-admin
#
# Output: a single JWT string on stdout, suitable for
#   curl -H "Authorization: Bearer $(...)" ...
set -euo pipefail

TENANT_ID="${1:?usage: gen-jwt.sh <tenant_uuid> [sub] [roles_csv]}"
SUBJECT="${2:-e2e-runner}"
ROLES_CSV="${3:-platform-admin}"

ISSUER="${JWT_ISS:-paladin-dev}"
AUDIENCE="${JWT_AUD:-paladin-api}"
SECRET="${JWT_HMAC:-dev-secret-change-me-32-bytes-min}"
TTL_SECONDS="${JWT_TTL:-3600}"

# Roles as a JSON array.
roles_json=$(
    printf '%s\n' "$ROLES_CSV" | awk -F, '{
        printf "["
        for (i = 1; i <= NF; i++) {
            gsub(/^ +| +$/, "", $i)
            printf "%s\"%s\"", (i > 1 ? "," : ""), $i
        }
        printf "]"
    }'
)

now=$(date +%s)
exp=$((now + TTL_SECONDS))

header='{"alg":"HS256","typ":"JWT"}'
payload=$(printf '{"iss":"%s","aud":"%s","sub":"%s","tenant":"%s","roles":%s,"iat":%d,"exp":%d}' \
    "$ISSUER" "$AUDIENCE" "$SUBJECT" "$TENANT_ID" "$roles_json" "$now" "$exp")

# base64url-encode (no padding) helper
b64url() {
    openssl base64 -e -A | tr '+/' '-_' | tr -d '='
}

h_enc=$(printf %s "$header" | b64url)
p_enc=$(printf %s "$payload" | b64url)
sig=$(printf %s "${h_enc}.${p_enc}" \
    | openssl dgst -sha256 -hmac "$SECRET" -binary \
    | b64url)

printf '%s.%s.%s\n' "$h_enc" "$p_enc" "$sig"
