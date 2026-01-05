# 1. Create Initial Object with External Ref
POST {{host}}/v1/objects
Host: paladin.local
{
  "content_type": "application/json",
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
{
  "content_type": "application/json",
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
{
  "content_type": "text/plain",
  "size_bytes": 500,
  "external_ref": "idem-key-001"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "validation_error"
jsonpath "$.details" contains "conflict"
