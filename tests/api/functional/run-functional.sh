#!/usr/bin/env bash
set -eo pipefail

HOST=${HOST:-"http://127.0.0.1:9090"}
PROTO="proto/paladin.proto"
AUTH_HEADER="authorization: Bearer test"
TENANT_ID="test-test-tenant"

echo "Running COMPREHENSIVE buf curl functional tests against $HOST..."

# Helper for separator
separator() {
  echo "------------------------------------------------"
  echo "$1"
  echo "------------------------------------------------"
}

# 1. Tenant Lifecycle
separator "1. Tenant Lifecycle"

echo "Creating Tenant..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"display_name\": \"Test Full Tenant\"}" \
  "$HOST/paladin.v1.Paladin/CreateTenant"

echo "Getting Tenant..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -d "{\"tenant_id\": \"$TENANT_ID\"}" \
  "$HOST/paladin.v1.Paladin/GetTenant"

echo "Listing Tenants..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -d '{"limit": 5}' \
  "$HOST/paladin.v1.Paladin/ListTenants"

echo "Patching Tenant Metadata..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"labels_patch_json\": \"{\\\"env\\\": \\\"test\\\"}\"}" \
  "$HOST/paladin.v1.Paladin/PatchTenantMetadata"

# 2. Category Management
separator "2. Category Management"

echo "Creating Category..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"slug\": \"full-test-cat\", \"name\": \"Full Test Category\"}" \
  "$HOST/paladin.v1.Paladin/CreateCategory"

echo "Getting Category..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"slug\": \"full-test-cat\"}" \
  "$HOST/paladin.v1.Paladin/GetCategory"

echo "Listing Categories..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"limit\": 10}" \
  "$HOST/paladin.v1.Paladin/ListCategories"

echo "Getting Category Stats..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"slug\": \"full-test-cat\"}" \
  "$HOST/paladin.v1.Paladin/GetCategoryStats"

# 3. Object Upload Flows (Single)
separator "3. Object Upload Flow (Single)"

echo "Creating Object..."
OBJ_RESP=$(buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"content_type\": \"text/plain\", \"size_bytes\": 100, \"category\": \"full-test-cat\", \"external_ref\": \"ext-$(date +%s)\"}" \
  "$HOST/paladin.v1.Paladin/CreateObject")
echo "$OBJ_RESP"

OBJ_ID=$(echo "$OBJ_RESP" | jq -r '.objectId')

# 4. Object Lifecycle
separator "4. Object Lifecycle"

echo "Getting Object..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_id\": \"$OBJ_ID\"}" \
  "$HOST/paladin.v1.Paladin/GetObject"

echo "Getting Object Meta..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_id\": \"$OBJ_ID\"}" \
  "$HOST/paladin.v1.Paladin/GetObjectMeta"

echo "Patching Object Meta..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_id\": \"$OBJ_ID\", \"labels\": {\"fuzzed\": \"true\"}}" \
  "$HOST/paladin.v1.Paladin/PatchObjectMeta"

echo "Completing Object..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_id\": \"$OBJ_ID\"}" \
  "$HOST/paladin.v1.Paladin/CompleteObject"

echo "Getting Object Stats..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\"}" \
  "$HOST/paladin.v1.Paladin/GetObjectStats"

echo "Listing Objects..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"limit\": 10}" \
  "$HOST/paladin.v1.Paladin/ListObjects"

# 5. Multipart Upload Flow
separator "5. Multipart Upload Flow"

echo "Initiating Multipart..."
MP_RESP=$(buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"content_type\": \"application/octet-stream\", \"size_bytes\": 10000000, \"category\": \"full-test-cat\"}" \
  "$HOST/paladin.v1.Paladin/InitiateMultipart")
echo "$MP_RESP"

UPLOAD_ID=$(echo "$MP_RESP" | jq -r '.uploadId')
MP_OBJ_ID=$(echo "$MP_RESP" | jq -r '.objectId')

echo "Signing Part 1..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"upload_id\": \"$UPLOAD_ID\", \"part_number\": 1}" \
  "$HOST/paladin.v1.Paladin/SignPart"

echo "Aborting Multipart..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"upload_id\": \"$UPLOAD_ID\"}" \
  "$HOST/paladin.v1.Paladin/AbortMultipart"

# 6. Bulk Operations
separator "6. Bulk Operations"

echo "Bulk Patching Objects..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"items\": [{\"object_id\": \"$OBJ_ID\", \"labels\": {\"bulk\": \"updated\"}}]}" \
  "$HOST/paladin.v1.Paladin/BulkPatchObjects"

echo "Bulk Deleting Objects..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_ids\": [\"$OBJ_ID\"]}" \
  "$HOST/paladin.v1.Paladin/BulkDeleteObjects"

echo "Bulk Restoring Objects..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_ids\": [\"$OBJ_ID\"]}" \
  "$HOST/paladin.v1.Paladin/BulkRestoreObjects"

echo "Bulk Purging Objects..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"object_ids\": [\"$OBJ_ID\"]}" \
  "$HOST/paladin.v1.Paladin/BulkPurgeObjects"

# 7. Cleanup
separator "7. Cleanup"

echo "Deleting Category..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -H "$AUTH_HEADER" \
  -d "{\"tenant_id\": \"$TENANT_ID\", \"slug\": \"full-test-cat\"}" \
  "$HOST/paladin.v1.Paladin/DeleteCategory"

echo "Deleting Tenant..."
buf curl --schema "$PROTO" --protocol connect --http2-prior-knowledge \
  -d "{\"tenant_id\": \"$TENANT_ID\"}" \
  "$HOST/paladin.v1.Paladin/DeleteTenant"

echo "All COMPREHENSIVE tests completed successfully."
