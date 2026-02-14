# Patch Object Metadata Tests
# Tests PATCH /v1/objects/{id}/meta for updating labels and external_ref

# Setup: Create test object
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 100,
  "labels": {
    "env": "dev",
    "version": "1.0"
  },
  "external_ref": "patch-test-original"
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"

# Test 1: Update labels only
PATCH {{host}}/v1/objects/{{object_id}}/meta
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "labels": {
    "env": "production",
    "version": "2.0",
    "team": "backend"
  }
}
HTTP 200
[Asserts]
jsonpath "$.labels.env" == "production"
jsonpath "$.labels.version" == "2.0"
jsonpath "$.labels.team" == "backend"
jsonpath "$.external_ref" == "patch-test-original"

# Test 2: Create another object for conflict test
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 50,
  "external_ref": "conflict-ref"
}
HTTP 201
[Captures]
conflict_obj_id: jsonpath "$.object_id"

# Test 6: Attempt to change external_ref to existing value (conflict)
PATCH {{host}}/v1/objects/{{object_id}}/meta
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "external_ref": "conflict-ref"
}
HTTP 400
[Asserts]
jsonpath "$.error.code" exists
jsonpath "$.error.message" exists

# Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204

DELETE {{host}}/v1/objects/{{conflict_obj_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204
