# 1. Create Object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
{
  "content_type": "text/plain",
  "size_bytes": 12
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"

# Upload content
PUT {{upload_url}}
Content-Type: text/plain
`soft delete me`
HTTP 200

# Complete object
POST {{host}}/v1/objects/{{object_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 200

# 2. Soft Delete (PATCH)
PATCH {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
{
  "status": "soft_deleted"
}
HTTP 200
[Asserts]
jsonpath "$.status" == "soft_deleted"

# 3. Verify Status is soft_deleted
GET {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 200
[Asserts]
jsonpath "$.status" == "soft_deleted"

# 4. Verify Download is NOT allowed (since status is not complete)
POST {{host}}/v1/objects/{{object_id}}/sign-download
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
{}
HTTP 409
[Asserts]
jsonpath "$.error.code" == "conflict"

# 5. Hard Delete (Purge)
DELETE {{host}}/v1/objects/{{object_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 204

# 6. Verify Hard Deleted (status is hard_deleted)
# After purge, the object's status is hard_deleted but still returned by GET
GET {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 200
[Asserts]
jsonpath "$.status" == "hard_deleted"

# 7. Idempotency Check with Soft Delete
# Create another object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
{
  "content_type": "text/plain",
  "size_bytes": 12
}
HTTP 201
[Captures]
object_id_2: jsonpath "$.object_id"
upload_url_2: jsonpath "$.upload.url"

# Soft delete with Idempotency Key
PATCH {{host}}/v1/objects/{{object_id_2}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
Idempotency-Key: test-idem-key-1
{
  "status": "soft_deleted"
}
HTTP 200
[Asserts]
jsonpath "$.status" == "soft_deleted"

# Retry same soft delete (should be 200)
PATCH {{host}}/v1/objects/{{object_id_2}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
Idempotency-Key: test-idem-key-1
{
  "status": "soft_deleted"
}
HTTP 200
[Asserts]
jsonpath "$.status" == "soft_deleted"

# Verify soft deleted
GET {{host}}/v1/objects/{{object_id_2}}
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 200
[Asserts]
jsonpath "$.status" == "soft_deleted"

# Cleanup
# Cleanup (Purge)
DELETE {{host}}/v1/objects/{{object_id_2}}/purge
Host: api.paladin.local
X-Tenant-ID: test-soft-delete
HTTP 204
