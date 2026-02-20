# 1. Basic Object Lifecycle
# -------------------------

# Create Single Object (Minimal)
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/jpeg",
  "size_bytes": 13
}
HTTP 201
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.upload.method" == "PUT"
# Verify TTL (default 15m = 900s)
jsonpath "$.upload.url" contains "X-Amz-Expires=900"

# Upload Content
PUT {{upload_url}}
Content-Type: image/jpeg
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
jsonpath "$.download_url" not exists

# Sign Download
POST {{host}}/v1/objects/{{object_id}}/sign-download
Host: api.paladin.local
X-Tenant-ID: test-tenant
{}
HTTP 200
[Asserts]
jsonpath "$.download.url" exists

# Cleanup (Purge)
DELETE {{host}}/v1/objects/{{object_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204


# 2. Metadata & New Endpoints
# ---------------------------

# Create Object with Metadata
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 1024,
  "labels": {
    "env": "test",
    "project": "acme"
  },
  "external_ref": "ref-objects-{{object_id}}"
}
HTTP 201
[Captures]
meta_object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"
[Asserts]
jsonpath "$.object_key" exists

# Get Object (Standard) - Check Metadata Preserved
GET {{host}}/v1/objects/{{meta_object_id}}
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{meta_object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.labels.project" == "acme"
jsonpath "$.external_ref" == "ref-objects-{{object_id}}"
jsonpath "$.download_url" not exists

# Get Object via Meta Endpoint (New) - Check No Download URL
GET {{host}}/v1/objects/{{meta_object_id}}/meta
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{meta_object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.external_ref" == "ref-objects-{{object_id}}"
jsonpath "$.download_url" not exists
jsonpath "$.status" == "pending"

# Upload Content
PUT {{upload_url}}
Content-Type: image/png
x-amz-server-side-encryption: AES256
`hello world png`
HTTP 200

# Mark Complete
POST {{host}}/v1/objects/{{meta_object_id}}/complete
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# Verify Final Meta
GET {{host}}/v1/objects/{{meta_object_id}}/meta
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# Cleanup (Purge)
DELETE {{host}}/v1/objects/{{meta_object_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204
