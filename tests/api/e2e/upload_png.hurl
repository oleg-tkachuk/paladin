# Upload PNG File Test
# Tests creating a PNG object, uploading content, marking it complete, and deleting it

# Create Single PNG Object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 11
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"
[Asserts]
jsonpath "$.object_tag" exists
jsonpath "$.upload.method" == "PUT"
jsonpath "$.upload.url" exists

# Get Object Metadata
GET {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "pending"
jsonpath "$.content_type" == "image/png"
jsonpath "$.size_bytes" == 11

# Upload Content
PUT {{upload_url}}
Content-Type: image/png
x-amz-server-side-encryption: AES256
`hello world`
HTTP 200

# Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# Verify Final State
GET {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"
jsonpath "$.content_type" == "image/png"

# Delete Object
DELETE {{host}}/v1/objects/{{object_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204

# Verify Object is Deleted
GET {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "hard_deleted"
