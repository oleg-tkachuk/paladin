#!/usr/bin/env bash
# Runs every hurl file in this directory against a live PALADIN backend.
#
# A fresh tenant + bucket + object_key is provisioned for each invocation
# via the helper RPCs — the per-flow .hurl files only assert their own
# slice of the state machine.  Reverse-order cleanup always runs.
#
# Environment
#   PALADIN_HOST          base URL (default http://127.0.0.1:8080)
#   PALADIN_BACKEND_ID    storage backend ID from config (default "primary")
#   PALADIN_BUCKET_NAME   physical S3 bucket (default "paladin-e2e-hurl")
#
# Requires: hurl >= 4.x, jq, openssl, uuidgen, curl
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$DIR/../lib"
# shellcheck source=tests/api/lib/connect.sh
source "$LIB/connect.sh"

PALADIN_BACKEND_ID="${PALADIN_BACKEND_ID:-primary}"
PALADIN_BUCKET_NAME="${PALADIN_BUCKET_NAME:-paladin-e2e-hurl}"
PALADIN_OBJECT_KEY="${PALADIN_OBJECT_KEY:-e2e-hurl}"
PAYLOAD_FILE="$DIR/fixtures/hello.txt"

if [[ ! -f "$PAYLOAD_FILE" ]]; then
    echo "missing fixture: $PAYLOAD_FILE" >&2
    exit 1
fi
PAYLOAD_LEN=$(wc -c <"$PAYLOAD_FILE" | tr -d ' ')

TENANT_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
JWT="$("$LIB/gen-jwt.sh" "$TENANT_ID")"
KEY="e2e/hurl-$(date +%s).txt"
# Per-run ephemeral bucket name for bucket.hurl's CRUD round-trip — keeps
# it isolated from the shared $PALADIN_BUCKET_NAME used by the rest of the
# suite (which can't be deleted while object_keys still reference it).
EPHEMERAL_BUCKET="paladin-e2e-eph-$(date +%s)"
export TENANT_ID JWT

cleanup() {
    local rc=$?
    set +e
    echo
    echo "─── Cleanup (tenant=$TENANT_ID) ───"
    rpc paladin.v1.ObjectKeyService/DeleteObjectKey \
        "{\"name\":\"object_keys/$PALADIN_OBJECT_KEY\",\"force\":true}" >/dev/null 2>&1
    rpc paladin.v1.BucketService/DeleteBucket \
        "{\"name\":\"backends/$PALADIN_BACKEND_ID/buckets/$PALADIN_BUCKET_NAME\"}" >/dev/null 2>&1
    rpc paladin.v1.BucketService/DeleteBucket \
        "{\"name\":\"backends/$PALADIN_BACKEND_ID/buckets/$EPHEMERAL_BUCKET\"}" >/dev/null 2>&1
    rpc paladin.v1.TenantService/DeleteTenant \
        "{\"name\":\"tenants/$TENANT_ID\"}" >/dev/null 2>&1
    exit $rc
}
trap cleanup EXIT

run_hurl() {
    local file="$1"
    shift
    echo
    echo "▶ $file"
    hurl --test \
        --variable host="$PALADIN_HOST" \
        --variable jwt="$JWT" \
        --variable tenant_id="$TENANT_ID" \
        --variable backend_id="$PALADIN_BACKEND_ID" \
        --variable bucket_name="$PALADIN_BUCKET_NAME" \
        --variable ephemeral_bucket="$EPHEMERAL_BUCKET" \
        --variable object_key="$PALADIN_OBJECT_KEY" \
        --variable key="$KEY" \
        --variable payload_len="$PAYLOAD_LEN" \
        "$@" \
        "$DIR/$file"
}

# ─── Phase 1: probes & system metadata (no fixtures required) ─────────────
run_hurl health.hurl
run_hurl system.hurl

# ─── Phase 2: tenant + bucket + object_key setup ──────────────────────────
# tenant.hurl creates and immediately deletes its own tenant. The rest of
# this script needs a *different* persistent tenant — that one is what
# the trap cleans up. We bootstrap it directly via Connect:
echo
echo "▶ bootstrap tenant via TenantService/CreateTenant"
rpc paladin.v1.TenantService/CreateTenant "{
    \"tenant_id\": \"$TENANT_ID\",
    \"display_name\": \"E2E orchestrator tenant\"
}" >/dev/null

run_hurl tenant.hurl    --variable tenant_id="$(uuidgen | tr '[:upper:]' '[:lower:]')"
run_hurl bucket.hurl
echo
echo "▶ re-create bucket for the rest of the run"
rpc paladin.v1.BucketService/CreateBucket "{
    \"backend_id\": \"$PALADIN_BACKEND_ID\",
    \"bucket_name\": \"$PALADIN_BUCKET_NAME\",
    \"display_name\": \"E2E orchestrator bucket\"
}" >/dev/null

run_hurl object_key.hurl
echo
echo "▶ re-create object_key for downstream object lifecycle tests"
rpc paladin.v1.ObjectKeyService/CreateObjectKey "{
    \"object_key\": \"$PALADIN_OBJECT_KEY\",
    \"display_name\": \"E2E orchestrator namespace\",
    \"backend_id\": \"$PALADIN_BACKEND_ID\",
    \"bucket_name\": \"$PALADIN_BUCKET_NAME\"
}" >/dev/null

# ─── Phase 3: object lifecycle + multipart + tag CRUD ─────────────────────
run_hurl object_lifecycle.hurl
run_hurl multipart.hurl
run_hurl object_tag.hurl

echo
echo "════════════════════════════════════════"
echo "ALL HURL E2E TESTS PASSED"
echo "════════════════════════════════════════"
