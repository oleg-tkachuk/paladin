# List Objects API Tests
# Tests GET /v1/objects with various filters and pagination

# Setup: Create test objects with different statuses and metadata
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 12,
  "labels": {
    "env": "test",
    "type": "list-test"
  },
  "external_ref": "list-test-1"
}
HTTP 201
[Captures]
obj1_id: jsonpath "$.object_id"
upload1_url: jsonpath "$.upload.url"

POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 200,
  "labels": {
    "env": "production",
    "type": "list-test"
  },
  "external_ref": "list-test-2"
}
HTTP 201
[Captures]
obj2_id: jsonpath "$.object_id"
upload2_url: jsonpath "$.upload.url"

# Upload and complete first object
PUT {{upload1_url}}
Content-Type: text/plain
x-amz-server-side-encryption: AES256
`test content`
HTTP 200

POST {{host}}/v1/objects/{{obj1_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200

# Test 1: List all objects (default pagination)
GET {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists
jsonpath "$.items" isCollection

# Test 2: List with limit
GET {{host}}/v1/objects?limit=10
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists
jsonpath "$.items" count <= 10

# Test 3: Filter by status - complete
GET {{host}}/v1/objects?status=complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists
jsonpath "$.items[*].status" includes "complete"
jsonpath "$.items[?(@.object_id == '{{obj1_id}}')]" exists

# Test 4: Filter by status - pending
GET {{host}}/v1/objects?status=pending
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists
jsonpath "$.items[?(@.object_id == '{{obj2_id}}')]" exists

# Test 5: Filter by external_ref
GET {{host}}/v1/objects?external_ref=list-test-1
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists
jsonpath "$.items" count == 1
jsonpath "$.items[0].external_ref" == "list-test-1"
jsonpath "$.items[0].object_id" == "{{obj1_id}}"

# Test 6: Pagination with cursor
GET {{host}}/v1/objects?limit=1
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" count == 1
jsonpath "$.pagination.next_cursor" exists
[Captures]
cursor: jsonpath "$.pagination.next_cursor"

GET {{host}}/v1/objects?limit=1&cursor={{cursor}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" exists

# Test 7: Filter by non-existent external_ref (empty result)
GET {{host}}/v1/objects?external_ref=non-existent-ref
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.items" count == 0

# Cleanup (Purge)
DELETE {{host}}/v1/objects/{{obj1_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204

# Cleanup (Purge)
DELETE {{host}}/v1/objects/{{obj2_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204
