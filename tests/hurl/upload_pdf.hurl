# Upload PDF File Test
# Tests creating a PDF object, uploading content, marking it complete, and deleting it

# Create Single PDF Object
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "application/pdf",
  "size_bytes": 11
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.upload.method" == "PUT"
jsonpath "$.upload.url" exists

# Get Object Metadata
GET {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "pending"
jsonpath "$.content_type" == "application/pdf"
jsonpath "$.size_bytes" == 11

# Upload Content
PUT {{upload_url}}
Content-Type: application/pdf
x-amz-server-side-encryption: AES256
`hello world`
HTTP 200

# Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# Verify Final State
GET {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"
jsonpath "$.content_type" == "application/pdf"

# Delete Object
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204

# Verify Object is Deleted
GET {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"
