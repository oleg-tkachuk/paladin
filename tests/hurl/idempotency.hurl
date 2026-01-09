# 1. Create Initial Object with External Ref
POST {{host}}/v1/objects
Host: paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 500,
  "external_ref": "idem-key-001"
}
HTTP 200
[Captures]
id_1: jsonpath "$.object_id"
[Asserts]
jsonpath "$.object_id" exists

# 2. Retry with SAME parameters (Should be Idempotent)
POST {{host}}/v1/objects
Host: paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 500,
  "external_ref": "idem-key-001"
}
HTTP 200
[Captures]
id_2: jsonpath "$.object_id"
[Asserts]
variable "id_1" == "{{id_2}}"

# 3. Retry with DIFFERENT parameters (Should Fail)
POST {{host}}/v1/objects
Host: paladin.local
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 600,
  "external_ref": "idem-key-001"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "Bad Request"
jsonpath "$.details" contains "conflict"

# Cleanup
DELETE {{host}}/v1/objects/{{id_1}}
Host: paladin.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"
