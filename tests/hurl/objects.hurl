# 1. Basic Object Lifecycle
# -------------------------

# Create Single Object (Minimal)
POST {{host}}/v1/objects
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/jpeg",
  "size_bytes": 13
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload_url"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.method" == "PUT"
# Verify SSE headers (default config uses AES256)
jsonpath "$.headers['x-amz-server-side-encryption']" == "AES256"
# Verify TTL (default 15m = 900s)
jsonpath "$.upload_url" includes "X-Amz-Expires=900"

# Get Object Metadata (Standard)
GET {{host}}/v1/objects/{{object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "pending"
# Optional fields should be missing or null if not provided
jsonpath "$.labels" not exists
jsonpath "$.external_ref" not exists

# Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Verify Final State
GET {{host}}/v1/objects/{{object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Cleanup (Delete)
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"


# 2. Metadata & New Endpoints
# ---------------------------

# Create Object with Metadata
POST {{host}}/v1/objects
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 1024,
  "labels": {
    "env": "test",
    "project": "acme"
  },
  "external_ref": "ref-123"
}
HTTP 200
[Captures]
meta_object_id: jsonpath "$.object_id"
[Asserts]
jsonpath "$.object_key" exists

# Get Object (Standard) - Check Metadata Preserved
GET {{host}}/v1/objects/{{meta_object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{meta_object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.labels.project" == "acme"
jsonpath "$.external_ref" == "ref-123"
jsonpath "$.download_url" exists

# Get Object via Meta Endpoint (New) - Check No Download URL
GET {{host}}/v1/objects/{{meta_object_id}}/meta
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{meta_object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.external_ref" == "ref-123"
jsonpath "$.download_url" not exists
jsonpath "$.status" == "pending"

# Mark Complete
POST {{host}}/v1/objects/{{meta_object_id}}/complete
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Verify Final Meta
GET {{host}}/v1/objects/{{meta_object_id}}/meta
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Cleanup (Delete)
DELETE {{host}}/v1/objects/{{meta_object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"
