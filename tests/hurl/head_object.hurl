# HEAD Object Tests
# Tests HEAD /v1/objects/{id} for lightweight existence checks

# Setup: Create test object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "application/pdf",
  "size_bytes": 1024
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"

# Test 1: HEAD request for pending object
HEAD {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200

# Upload content
PUT {{upload_url}}
Content-Type: application/pdf
x-amz-server-side-encryption: AES256
`test pdf content`
HTTP 200

# Complete object
POST {{host}}/v1/objects/{{object_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200

# Test 2: HEAD request for completed object
HEAD {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200

# Test 3: HEAD request for non-existent object
HEAD {{host}}/v1/objects/00000000-0000-0000-0000-000000000000
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 404

# Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204

# Test 4: HEAD request for deleted object
HEAD {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
