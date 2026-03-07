#!/usr/bin/env bash
# tests/grpcurl/cases/enhanced_features.sh
set -e

# Use unique ID for this test run
TEST_TENANT="enhanced-test-$(date +%s%N)"
echo "Testing Enhanced Features for tenant: ${TEST_TENANT}"

# 1. Create hierarchical categories
echo "Creating hierarchical categories..."
grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"slug\": \"docs\",
  \"name\": \"Documents\"
}" localhost:9090 paladin.v1.Paladin/CreateCategory

grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"slug\": \"docs/invoices\",
  \"name\": \"Invoices\"
}" localhost:9090 paladin.v1.Paladin/CreateCategory

grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"slug\": \"docs/invoices/2024\",
  \"name\": \"2024 Invoices\"
}" localhost:9090 paladin.v1.Paladin/CreateCategory

# 2. Create objects in each category
echo "Creating objects in different categories..."
OBJ1_RESP=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"category\": \"docs\",
  \"content_type\": \"text/plain\",
  \"size_bytes\": 100
}" localhost:9090 paladin.v1.Paladin/CreateObject)
ID1=$(echo "${OBJ1_RESP}" | grep "objectId" | sed 's/.*"objectId": "\(.*\)".*/\1/')

OBJ2_RESP=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"category\": \"docs/invoices\",
  \"content_type\": \"text/plain\",
  \"size_bytes\": 200
}" localhost:9090 paladin.v1.Paladin/CreateObject)
ID2=$(echo "${OBJ2_RESP}" | grep "objectId" | sed 's/.*"objectId": "\(.*\)".*/\1/')

OBJ3_RESP=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"category\": \"docs/invoices/2024\",
  \"content_type\": \"text/plain\",
  \"size_bytes\": 300
}" localhost:9090 paladin.v1.Paladin/CreateObject)
ID3=$(echo "${OBJ3_RESP}" | grep "objectId" | sed 's/.*"objectId": "\(.*\)".*/\1/')

# 3. Test non-recursive list
echo "Listing 'docs' non-recursively..."
LIST_NON_REC=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"category\": \"docs\",
  \"recursive\": false
}" localhost:9090 paladin.v1.Paladin/ListObjects)
COUNT_NON_REC=$(echo "${LIST_NON_REC}" | grep "objectId" | wc -l)
echo "Non-recursive count: ${COUNT_NON_REC}"
if [ "${COUNT_NON_REC}" -ne 1 ]; then
  echo "FAIL: Expected 1 object, got ${COUNT_NON_REC}"
  exit 1
fi

# 4. Test recursive list
echo "Listing 'docs' recursively..."
LIST_REC=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"category\": \"docs\",
  \"recursive\": true
}" localhost:9090 paladin.v1.Paladin/ListObjects)
COUNT_REC=$(echo "${LIST_REC}" | grep "objectId" | wc -l)
echo "Recursive count: ${COUNT_REC}"
if [ "${COUNT_REC}" -ne 3 ]; then
  echo "FAIL: Expected 3 objects, got ${COUNT_REC}"
  exit 1
fi

# 5. Test BulkPatchObjects
echo "Bulk patching objects..."
grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"items\": [
    {
      \"object_id\": \"${ID1}\",
      \"labels\": {\"patched\": \"true\", \"p1\": \"v1\"},
      \"external_ref\": \"ext-ref-1\"
    },
    {
      \"object_id\": \"${ID2}\",
      \"labels\": {\"patched\": \"true\", \"p2\": \"v2\"}
    }
  ]
}" localhost:9090 paladin.v1.Paladin/BulkPatchObjects

# 6. Verify patch
echo "Verifying patch for ${ID1}..."
GET1_RESP=$(grpcurl -plaintext -H "x-tenant-id: ${TEST_TENANT}" -d "{
  \"tenant_id\": \"${TEST_TENANT}\",
  \"object_id\": \"${ID1}\"
}" localhost:9090 paladin.v1.Paladin/GetObject)

if [[ ! "${GET1_RESP}" =~ "patched" ]] || [[ ! "${GET1_RESP}" =~ "ext-ref-1" ]]; then
  echo "FAIL: Patch not applied to ${ID1}"
  echo "${GET1_RESP}"
  exit 1
fi

echo "Enhanced features E2E test passed!"
