# MultipartUploadService — initiate → presign part → abort.
# (We don't actually push 5+ MiB through curl in CI; this validates the
# control-plane handshake, not the storage path.)
#
# Variables: host, jwt, tenant_id, object_key

POST {{host}}/paladin.v1.MultipartUploadService/InitiateMultipartUpload
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "object_key": "{{object_key}}",
    "key": "e2e/multipart-bin",
    "content_type": "application/octet-stream",
    "size_bytes": 33554432,
    "checksum_algorithm": "CHECKSUM_ALGORITHM_CRC32C"
}
HTTP 200
[Captures]
upload_id:    jsonpath "$.uploadId"
object_name:  jsonpath "$.object.name"
[Asserts]
jsonpath "$.uploadId" exists
jsonpath "$.object.name" exists

# NOTE: MultipartUploadService.PresignPart is defined in proto but not yet
# wired in connectshim (returns Unimplemented / 501). Re-enable this block
# once the bridge lands.
#
# POST {{host}}/paladin.v1.MultipartUploadService/PresignPart
# Content-Type: application/json
# Connect-Protocol-Version: 1
# Authorization: Bearer {{jwt}}
# {
#     "object_name": "{{object_name}}",
#     "upload_id": "{{upload_id}}",
#     "part_number": 1
# }
# HTTP 200
# [Asserts]
# jsonpath "$.uploadUrl.url" exists

# Abort cleanly so we don't leak a multipart session.
POST {{host}}/paladin.v1.MultipartUploadService/AbortMultipartUpload
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "object_name": "{{object_name}}",
    "upload_id": "{{upload_id}}"
}
HTTP 200
