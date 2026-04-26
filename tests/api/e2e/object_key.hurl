# ObjectKeyService — tenant-scoped namespace bound to a backend+bucket.
#
# Variables:
#   host, jwt, tenant_id, backend_id, bucket_name (must already exist)
#   object_key — namespace slug to create

POST {{host}}/paladin.v1.ObjectKeyService/CreateObjectKey
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "object_key":   "{{object_key}}",
    "display_name": "E2E hurl namespace",
    "backend_id":   "{{backend_id}}",
    "bucket_name":  "{{bucket_name}}"
}
HTTP 200
[Asserts]
jsonpath "$.objectKey"  == "{{object_key}}"
jsonpath "$.backendId"  == "{{backend_id}}"
jsonpath "$.bucketName" == "{{bucket_name}}"

POST {{host}}/paladin.v1.ObjectKeyService/GetObjectKey
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "object_keys/{{object_key}}"}
HTTP 200
[Asserts]
jsonpath "$.objectKey" == "{{object_key}}"

POST {{host}}/paladin.v1.ObjectKeyService/ListObjectKeys
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"page_size": 50}
HTTP 200
[Asserts]
jsonpath "$.objectKeys" exists

POST {{host}}/paladin.v1.ObjectKeyService/GetObjectKeyStats
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "object_keys/{{object_key}}"}
HTTP 200
[Asserts]
# Proto's canonical JSON encoding omits zero-valued scalar fields. The
# always-emitted shape is the by-state map (AVAILABLE / PENDING / DELETED).
jsonpath "$.objectCountByState" exists
jsonpath "$.objectCountByState.AVAILABLE" exists

# Negative test: deleting a bucket that an ObjectKey still references must
# fail (FK-block). Connect maps FailedPrecondition → HTTP 412.
POST {{host}}/paladin.v1.BucketService/DeleteBucket
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "backends/{{backend_id}}/buckets/{{bucket_name}}"}
HTTP *
[Asserts]
status >= 400
status <= 499
jsonpath "$.code" exists

# Cleanup
POST {{host}}/paladin.v1.ObjectKeyService/DeleteObjectKey
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "object_keys/{{object_key}}"}
HTTP 200
