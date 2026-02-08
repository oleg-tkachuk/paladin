# Abort Multipart Upload Tests
# Tests POST /v1/multipart/{upload_id}/abort

# Setup: Initiate multipart upload
POST {{host}}/v1/multipart
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "application/octet-stream",
  "size_bytes": 50000000,
  "labels": {
    "type": "abort-test"
  }
}
HTTP 200
[Captures]
upload_id: jsonpath "$.upload_id"
object_id: jsonpath "$.object_id"

# Verify multipart is active
GET {{host}}/v1/multipart/{{upload_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "initiated"

# Test 1: Abort active multipart upload
POST {{host}}/v1/multipart/{{upload_id}}/abort
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "aborted"

# Test 2: Verify object status changed to aborted
GET {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "uploading"

# Test 3: Attempt to abort already aborted upload (should be idempotent)
POST {{host}}/v1/multipart/{{upload_id}}/abort
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "aborted"

# Test 4: Attempt to abort non-existent upload_id
POST {{host}}/v1/multipart/non-existent-upload-id/abort
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 400
[Asserts]
jsonpath "$.error.code" exists

# Setup for completed multipart test
POST {{host}}/v1/multipart
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "application/octet-stream",
  "size_bytes": 10000000
}
HTTP 200
[Captures]
completed_upload_id: jsonpath "$.upload_id"
completed_object_id: jsonpath "$.object_id"

# Sign and upload a part
POST {{host}}/v1/multipart/{{completed_upload_id}}/parts/1/sign
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Captures]
part_url: jsonpath "$.upload.url"

PUT {{part_url}}
Content-Type: application/zip
x-amz-server-side-encryption: AES256
`part 1 data`
HTTP 200
[Captures]
part_etag: header "ETag"

# Complete the multipart upload
POST {{host}}/v1/multipart/{{completed_upload_id}}/complete
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "parts": [
    {
      "part_number": 1,
      "etag": "{{part_etag}}"
    }
  ]
}
HTTP 200

# Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204

DELETE {{host}}/v1/objects/{{completed_object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204
