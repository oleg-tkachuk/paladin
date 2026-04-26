# BucketService — provision physical S3 bucket inside the configured backend.
#
# Variables:
#   host         — base URL
#   jwt          — bearer JWT
#   tenant_id    — UUID (BucketService is admin-scoped; the JWT carries
#                  the tenant for context but buckets are tenant-agnostic)
#   backend_id   — must match `storage.backends.<id>` in config (default "primary")
#   bucket_name  — physical S3 bucket to create

POST {{host}}/paladin.v1.BucketService/CreateBucket
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "backend_id": "{{backend_id}}",
    "bucket_name": "{{ephemeral_bucket}}",
    "display_name": "E2E hurl bucket"
}
HTTP 200
[Asserts]
jsonpath "$.backendId"  == "{{backend_id}}"
jsonpath "$.bucketName" == "{{ephemeral_bucket}}"
jsonpath "$.name"       == "backends/{{backend_id}}/buckets/{{ephemeral_bucket}}"

POST {{host}}/paladin.v1.BucketService/GetBucket
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "backends/{{backend_id}}/buckets/{{ephemeral_bucket}}"}
HTTP 200
[Asserts]
jsonpath "$.bucketName" == "{{ephemeral_bucket}}"

POST {{host}}/paladin.v1.BucketService/ListBuckets
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"backend_id": "{{backend_id}}", "page_size": 50}
HTTP 200
[Asserts]
jsonpath "$.buckets" exists

# Delete (no objects yet, no ObjectKey FK referencing → safe).
POST {{host}}/paladin.v1.BucketService/DeleteBucket
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "backends/{{backend_id}}/buckets/{{ephemeral_bucket}}"}
HTTP 200
