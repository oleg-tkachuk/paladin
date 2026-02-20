# 1. Create Initial Object with External Ref
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 500,
  "external_ref": "idem-key-run-unique"
}
HTTP 201
[Captures]
id_1: jsonpath "$.object_id"
[Asserts]
jsonpath "$.object_id" exists

# 2. Retry with SAME parameters (Should be Idempotent)
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 500,
  "external_ref": "idem-key-run-unique"
}
HTTP 201
[Captures]
id_2: jsonpath "$.object_id"
[Asserts]
variable "id_1" == "{{id_2}}"

# 3. Retry with DIFFERENT parameters (Should Fail)
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 600,
  "external_ref": "idem-key-run-unique"
}
HTTP 409
[Asserts]
jsonpath "$.error.code" == "conflict"
jsonpath "$.error.message" contains "external_ref"
jsonpath "$.error.request_id" exists
jsonpath "$.error.trace_id" exists

# Cleanup
DELETE {{host}}/v1/objects/{{id_1}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204

# 4. Create Object with Idempotency-Key Header
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
Idempotency-Key: unique-key-12345678
{
  "content_type": "text/plain",
  "size_bytes": 100
}
HTTP 201
[Captures]
idem_header_id: jsonpath "$.object_id"

# 5. Retry with SAME Idempotency-Key Header
POST {{host}}/v1/objects
Host: api.paladin.local
X-Tenant-ID: test-tenant
Idempotency-Key: unique-key-12345678
{
  "content_type": "text/plain",
  "size_bytes": 100
}
HTTP 201
[Asserts]
jsonpath "$.object_id" == "{{idem_header_id}}"

# Cleanup
DELETE {{host}}/v1/objects/{{idem_header_id}}/purge
Host: api.paladin.local
X-Tenant-ID: test-tenant
HTTP 204
