#!/usr/bin/env bash
# End-to-end smoke test exercising every Connect service against a running
# PALADIN backend.  Asserts the full hierarchy:
#
#   storage_backend (config-seeded)
#     └── bucket  (created via BucketService)
#           └── object_key (created via ObjectKeyService, FK to bucket)
#                 └── object  (uploaded via ObjectService → S3 PUT → CompleteObject)
#
# The script is **idempotent**: it generates a fresh tenant UUID on every
# run, exercises every flow under that tenant, then deletes everything in
# reverse order so re-running on the same backend works.
#
# Environment
#   PALADIN_HOST          base URL (default http://127.0.0.1:8080)
#   PALADIN_BACKEND_ID    storage backend to provision against (default "primary")
#   PALADIN_BUCKET_NAME   physical S3 bucket to create (default "paladin-e2e")
#
# Requires: bash, curl, jq, openssl
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="$DIR/../lib"
# shellcheck source=tests/api/lib/connect.sh
source "$LIB/connect.sh"

export PALADIN_BACKEND_ID="${PALADIN_BACKEND_ID:-primary}"
export PALADIN_BUCKET_NAME="${PALADIN_BUCKET_NAME:-paladin-e2e}"
export PALADIN_OBJECT_KEY="${PALADIN_OBJECT_KEY:-e2e-prefix}"

# A fresh tenant UUID per run so reruns don't trip the unique constraint.
TENANT_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
export TENANT_ID
JWT="$("$LIB/gen-jwt.sh" "$TENANT_ID")"
export JWT

cleanup() {
    local rc=$?
    set +e
    banner "Cleanup (tenant=$TENANT_ID)"
    rpc paladin.v1.ObjectKeyService/DeleteObjectKey \
        "{\"name\":\"object_keys/$PALADIN_OBJECT_KEY\",\"force\":true}" >/dev/null 2>&1 || true
    rpc paladin.v1.BucketService/DeleteBucket \
        "{\"name\":\"backends/$PALADIN_BACKEND_ID/buckets/$PALADIN_BUCKET_NAME\"}" >/dev/null 2>&1 || true
    rpc paladin.v1.TenantService/DeleteTenant \
        "{\"name\":\"tenants/$TENANT_ID\"}" >/dev/null 2>&1 || true
    exit $rc
}
trap cleanup EXIT

echo "Running functional smoke against $PALADIN_HOST  (tenant=$TENANT_ID)"

# ─── 1. Tenant lifecycle ──────────────────────────────────────────────────
banner "1. Tenant lifecycle"

resp=$(rpc paladin.v1.TenantService/CreateTenant "{
    \"tenant_id\":\"$TENANT_ID\",
    \"display_name\":\"E2E tenant\"
}")
echo "$resp" | jq .
assert_jq '.tenantId == "'"$TENANT_ID"'"' <<<"$resp"

rpc paladin.v1.TenantService/GetTenant "{\"name\":\"tenants/$TENANT_ID\"}" >/dev/null
echo "  ✓ GetTenant"

rpc paladin.v1.TenantService/ListTenants '{"page_size":5}' >/dev/null
echo "  ✓ ListTenants"

# ─── 2. Bucket lifecycle (BucketService) ──────────────────────────────────
banner "2. Bucket lifecycle"

resp=$(rpc paladin.v1.BucketService/CreateBucket "{
    \"backend_id\":\"$PALADIN_BACKEND_ID\",
    \"bucket_name\":\"$PALADIN_BUCKET_NAME\",
    \"display_name\":\"E2E bucket\"
}")
assert_jq '.backendId == "'"$PALADIN_BACKEND_ID"'"' <<<"$resp"
assert_jq '.bucketName == "'"$PALADIN_BUCKET_NAME"'"' <<<"$resp"

rpc paladin.v1.BucketService/GetBucket \
    "{\"name\":\"backends/$PALADIN_BACKEND_ID/buckets/$PALADIN_BUCKET_NAME\"}" >/dev/null
echo "  ✓ GetBucket"

rpc paladin.v1.BucketService/ListBuckets "{\"backend_id\":\"$PALADIN_BACKEND_ID\",\"page_size\":50}" >/dev/null
echo "  ✓ ListBuckets"

# ─── 3. ObjectKey lifecycle (ObjectKeyService) ────────────────────────────
banner "3. ObjectKey lifecycle"

resp=$(rpc paladin.v1.ObjectKeyService/CreateObjectKey "{
    \"object_key\":\"$PALADIN_OBJECT_KEY\",
    \"display_name\":\"E2E namespace\",
    \"backend_id\":\"$PALADIN_BACKEND_ID\",
    \"bucket_name\":\"$PALADIN_BUCKET_NAME\"
}")
assert_jq '.objectKey == "'"$PALADIN_OBJECT_KEY"'"' <<<"$resp"
assert_jq '.bucketName == "'"$PALADIN_BUCKET_NAME"'"' <<<"$resp"

rpc paladin.v1.ObjectKeyService/GetObjectKey "{\"name\":\"object_keys/$PALADIN_OBJECT_KEY\"}" >/dev/null
echo "  ✓ GetObjectKey"

rpc paladin.v1.ObjectKeyService/ListObjectKeys '{"page_size":50}' >/dev/null
echo "  ✓ ListObjectKeys"

rpc paladin.v1.ObjectKeyService/GetObjectKeyStats "{\"name\":\"object_keys/$PALADIN_OBJECT_KEY\"}" >/dev/null
echo "  ✓ GetObjectKeyStats"

# ─── 4. ObjectTag CRUD (ObjectTagService) ─────────────────────────────────
banner "4. ObjectTag CRUD"

rpc paladin.v1.ObjectTagService/CreateObjectTag '{
    "slug":"e2e",
    "display_name":"E2E tag",
    "description":"Ad-hoc taxonomy entry"
}' >/dev/null
echo "  ✓ CreateObjectTag"

rpc paladin.v1.ObjectTagService/ListObjectTags '{"page_size":50}' >/dev/null
echo "  ✓ ListObjectTags"

rpc paladin.v1.ObjectTagService/DeleteObjectTag '{"name":"object_tags/e2e"}' >/dev/null
echo "  ✓ DeleteObjectTag"

# ─── 5. Object upload → complete ──────────────────────────────────────────
# NOTE: ObjectService.GetObject / DownloadObject / DeleteObject / RestoreObject
# are not yet wired in the connectshim. Once they land we should extend
# this section with a body round-trip and soft-delete → restore → purge.
banner "5. Object lifecycle"

PAYLOAD="hello-world-$(date +%s)"
KEY="e2e/test-$(date +%s).txt"

resp=$(rpc paladin.v1.ObjectService/UploadObject "{
    \"object_key\":\"$PALADIN_OBJECT_KEY\",
    \"key\":\"$KEY\",
    \"content_type\":\"text/plain\",
    \"size_hint_bytes\":${#PAYLOAD},
    \"checksum_algorithm\":\"CHECKSUM_ALGORITHM_CRC32C\"
}")
assert_jq '.uploadUrl.url | length > 0' <<<"$resp"
UPLOAD_URL=$(jq -r '.uploadUrl.url' <<<"$resp")
OBJECT_NAME=$(jq -r '.object.name' <<<"$resp")
COMPLETION_MODE=$(jq -r '.completionMode' <<<"$resp")
echo "  ✓ UploadObject (mode=$COMPLETION_MODE name=$OBJECT_NAME)"

# Stream the body to S3.
HDR_ARGS=()
while IFS=$'\t' read -r k v; do
    [[ -z "$k" ]] && continue
    HDR_ARGS+=(-H "$k: $v")
done < <(jq -r '.uploadUrl.requiredHeaders // {} | to_entries[] | "\(.key)\t\(.value)"' <<<"$resp")
curl --fail-with-body --silent --show-error \
    -X PUT \
    "${HDR_ARGS[@]}" \
    -H "Content-Type: text/plain" \
    --data-binary "$PAYLOAD" \
    "$UPLOAD_URL" >/dev/null
echo "  ✓ S3 PUT"

# Promote PENDING → AVAILABLE (no-op when backend uses IMPLICIT events).
resp=$(rpc paladin.v1.ObjectService/CompleteObject "{\"name\":\"$OBJECT_NAME\"}")
assert_jq '.state == "OBJECT_STATE_AVAILABLE"' <<<"$resp"
echo "  ✓ CompleteObject"

# ─── 6. ListObjects + CountObjects ────────────────────────────────────────
banner "6. List / Count"

rpc paladin.v1.ObjectService/ListObjects "{
    \"object_key\":\"$PALADIN_OBJECT_KEY\",
    \"page_size\":50
}" >/dev/null
echo "  ✓ ListObjects"

rpc paladin.v1.ObjectService/CountObjects "{
    \"object_key\":\"$PALADIN_OBJECT_KEY\"
}" >/dev/null
echo "  ✓ CountObjects"

# ─── 7. Multipart lifecycle (initiate → presign part → abort) ─────────────
banner "7. Multipart lifecycle"

resp=$(rpc paladin.v1.MultipartUploadService/InitiateMultipartUpload "{
    \"object_key\":\"$PALADIN_OBJECT_KEY\",
    \"key\":\"e2e/big-$(date +%s).bin\",
    \"content_type\":\"application/octet-stream\",
    \"size_bytes\":33554432,
    \"checksum_algorithm\":\"CHECKSUM_ALGORITHM_CRC32C\"
}")
UPLOAD_ID=$(jq -r '.uploadId' <<<"$resp")
MP_OBJECT_NAME=$(jq -r '.object.name' <<<"$resp")
echo "  ✓ InitiateMultipartUpload (upload_id=$UPLOAD_ID)"

rpc paladin.v1.MultipartUploadService/PresignPart "{
    \"object_name\":\"$MP_OBJECT_NAME\",
    \"upload_id\":\"$UPLOAD_ID\",
    \"part_number\":1
}" >/dev/null
echo "  ✓ PresignPart"

rpc paladin.v1.MultipartUploadService/ListParts "{
    \"object_name\":\"$MP_OBJECT_NAME\",
    \"upload_id\":\"$UPLOAD_ID\"
}" >/dev/null
echo "  ✓ ListParts"

rpc paladin.v1.MultipartUploadService/AbortMultipartUpload "{
    \"object_name\":\"$MP_OBJECT_NAME\",
    \"upload_id\":\"$UPLOAD_ID\"
}" >/dev/null
echo "  ✓ AbortMultipartUpload"

banner "ALL FUNCTIONAL TESTS PASSED"
