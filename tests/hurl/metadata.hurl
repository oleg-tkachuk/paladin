# 1. Rich Metadata
# ----------------
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 100,
  "labels": {
    "env": "production",
    "project": "Project X",
    "owner": "Team Alpha",
    "special_chars": "!@#$%^&*()_+",
    "unicode": "こんにちは"
  },
  "external_ref": "ext-ref-metadata-v110"
}
HTTP 200
[Captures]
rich_id: jsonpath "$.object_id"
[Asserts]
jsonpath "$.object_id" exists

# Verify Rich Metadata via GET Object
GET {{host}}/v1/objects/{{rich_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{rich_id}}"
jsonpath "$.labels.env" == "production"
jsonpath "$.labels.project" == "Project X"
jsonpath "$.labels.owner" == "Team Alpha"
jsonpath "$.labels.special_chars" == "!@#$%^&*()_+"
jsonpath "$.labels.unicode" == "こんにちは"
jsonpath "$.external_ref" == "ext-ref-metadata-v110"
jsonpath "$.download_url" not exists

# Verify Rich Metadata via GET Meta
GET {{host}}/v1/objects/{{rich_id}}/meta
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{rich_id}}"
jsonpath "$.labels.env" == "production"
jsonpath "$.labels.unicode" == "こんにちは"
jsonpath "$.external_ref" == "ext-ref-metadata-v110"
jsonpath "$.download_url" not exists

# Cleanup Rich Object
DELETE {{host}}/v1/objects/{{rich_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204


# 2. Empty Metadata
# -----------------
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 50
}
HTTP 200
[Captures]
empty_meta_id: jsonpath "$.object_id"

# Verify Empty Metadata
GET {{host}}/v1/objects/{{empty_meta_id}}/meta
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{empty_meta_id}}"
# labels might be null or missing
jsonpath "$.labels" not exists
jsonpath "$.external_ref" not exists

# Cleanup Empty Meta Object
DELETE {{host}}/v1/objects/{{empty_meta_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204


# 3. Validation Errors
# --------------------
# Invalid Labels Type (Array instead of Map)
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 50,
  "labels": ["tag1", "tag2"]
}
HTTP 400
[Asserts]
jsonpath "$.error.code" == "bad_request"
jsonpath "$.error.request_id" exists

# Invalid Labels Type (String instead of Map)
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 50,
  "labels": "some-tag"
}
HTTP 400
[Asserts]
jsonpath "$.error.code" == "bad_request"
jsonpath "$.error.request_id" exists
