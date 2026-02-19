# Re-sign Upload URL Tests
# Tests POST /v1/objects/{id}/sign-upload for re-issuing upload URLs

# Setup: Create test object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/jpeg",
  "size_bytes": 500
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
original_upload_url: jsonpath "$.upload.url"

# Test 1: Re-sign upload for pending object
POST {{host}}/v1/objects/{{object_id}}/sign-upload
Host: api.paladin.local
X-Tenant-ID: test-tenant
{}
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.upload.url" exists
jsonpath "$.upload.method" == "PUT"
jsonpath "$.upload.expires_at" exists
[Captures]
new_upload_url: jsonpath "$.upload.url"

# Test 2: Re-sign with custom TTL
POST {{host}}/v1/objects/{{object_id}}/sign-upload
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "upload_expires_in_seconds": 300
}
HTTP 200
[Asserts]
jsonpath "$.upload.url" exists
jsonpath "$.upload.url" contains "X-Amz-Expires=300"

# Test 3: Verify new signed URL works for upload
PUT {{new_upload_url}}
Content-Type: image/jpeg
x-amz-server-side-encryption: AES256
`test image data`
HTTP 200

# Complete the object
POST {{host}}/v1/objects/{{object_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200

# Cleanup
DELETE {{host}}/v1/objects/{{object_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204
