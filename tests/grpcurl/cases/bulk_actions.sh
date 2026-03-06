#!/usr/bin/env bash
# tests/grpcurl/cases/bulk_actions.sh
set -e

# Use unique ID for this test run
TEST_TENANT="bulk-test-$(date +%s)"
echo "Testing Bulk Actions for tenant: ${TEST_TENANT}"

# 1. Create a category
echo "Creating category 'docs'..."
cat <<EOF | grpcurl -plaintext -d @ localhost:50051 paladin.Paladin/CreateCategory
{
  "tenant_id": "${TEST_TENANT}",
  "slug": "docs",
  "name": "Documents"
}
EOF

# 2. Bulk Create Objects
echo "Bulk creating 3 objects..."
BULK_CREATE_RESP=$(cat <<EOF | grpcurl -plaintext -d @ localhost:50051 paladin.Paladin/BulkCreateObjects
{
  "tenant_id": "${TEST_TENANT}",
  "items": [
    {"category": "docs", "content_type": "text/plain", "size_bytes": 100},
    {"category": "docs", "content_type": "image/png", "size_bytes": 200},
    {"category": "docs", "content_type": "application/pdf", "size_bytes": 300}
  ]
}
EOF
)

# Extract IDs (using a simple grep/sed since we don't assume jq)
OBJ_IDS=$(echo "${BULK_CREATE_RESP}" | grep "objectId" | sed 's/.*"objectId": "\(.*\)".*/\1/')
ID_ARRAY=($OBJ_IDS)

if [ "${#ID_ARRAY[@]}" -ne 3 ]; then
  echo "FAIL: Expected 3 object IDs, got ${#ID_ARRAY[@]}"
  exit 1
fi

echo "Created objects: ${ID_ARRAY[*]}"

# 3. Bulk Complete Objects
# (In a real scenario, we'd upload to S3 first, but our mock/test setup might allow completion)
echo "Bulk completing objects..."
OBJ_ID_JSON=$(printf '"%s",' "${ID_ARRAY[@]}" | sed 's/,$//')
cat <<EOF | grpcurl -plaintext -d @ localhost:50051 paladin.Paladin/BulkCompleteObjects
{
  "tenant_id": "${TEST_TENANT}",
  "object_ids": [${OBJ_ID_JSON}]
}
EOF

# 4. Bulk Delete Objects
echo "Bulk deleting objects..."
cat <<EOF | grpcurl -plaintext -d @ localhost:50051 paladin.Paladin/BulkDeleteObjects
{
  "tenant_id": "${TEST_TENANT}",
  "object_ids": [${OBJ_ID_JSON}]
}
EOF

echo "Bulk actions E2E test passed!"
