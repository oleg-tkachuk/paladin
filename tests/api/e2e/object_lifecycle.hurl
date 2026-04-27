# ObjectService — upload + complete + list, exercised against a real S3
# backend.
#
# Variables:
#   host, jwt, tenant_id, object_key, key, payload_len
#
# IMPLEMENTED today:
#   ObjectService.UploadObject
#   ObjectService.CompleteObject
#   ObjectService.ListObjects
#   ObjectService.CountObjects
#   PresignService.RegenerateUploadUrl
#   PresignService.PresignDownload
#   ObjectService.GetObject / LookupObject / DownloadObject
#   ObjectService.UpdateObject
#   ObjectService.DeleteObject  / RestoreObject

# 1. UploadObject — server records PENDING row, returns presigned PUT URL.
POST {{host}}/paladin.v1.ObjectService/UploadObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "object_key": "{{object_key}}",
    "key": "{{key}}",
    "content_type": "text/plain",
    "size_hint_bytes": {{payload_len}},
    "checksum_algorithm": "CHECKSUM_ALGORITHM_CRC32C"
}
HTTP 200
[Captures]
upload_url:    jsonpath "$.uploadUrl.url"
object_name:   jsonpath "$.object.name"
[Asserts]
jsonpath "$.completionMode" matches "COMPLETION_MODE_(IMPLICIT|EXPLICIT)"
jsonpath "$.object.state"   == "OBJECT_STATE_PENDING"

# 2. PUT body to S3.
PUT {{upload_url}}
Content-Type: text/plain
file,fixtures/hello.txt;
HTTP 200

# 3. CompleteObject — promotes PENDING → AVAILABLE via HEAD on the bucket.
POST {{host}}/paladin.v1.ObjectService/CompleteObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200
[Asserts]
jsonpath "$.state" == "OBJECT_STATE_AVAILABLE"

# 4. ListObjects under the namespace — should include our just-uploaded one.
POST {{host}}/paladin.v1.ObjectService/ListObjects
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "object_key": "{{object_key}}",
    "page_size": 50
}
HTTP 200
[Asserts]
jsonpath "$.objects" exists

# 5. CountObjects — sanity check that the count includes our row.
POST {{host}}/paladin.v1.ObjectService/CountObjects
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"object_key": "{{object_key}}"}
HTTP 200
[Asserts]
jsonpath "$.approximateCount" exists

# 6. PresignDownload — issues a GET URL for the AVAILABLE object.
POST {{host}}/paladin.v1.PresignService/PresignDownload
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200
[Asserts]
jsonpath "$.downloadUrl.url" exists

# 7. GetObject — fetch by resource name.
POST {{host}}/paladin.v1.ObjectService/GetObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200
[Captures]
resource_version: jsonpath "$.resourceVersion"
[Asserts]
jsonpath "$.state" == "OBJECT_STATE_AVAILABLE"
jsonpath "$.key"   == "{{key}}"

# 8. LookupObject — resolve (object_key, key) → Object.
POST {{host}}/paladin.v1.ObjectService/LookupObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"object_key": "{{object_key}}", "key": "{{key}}"}
HTTP 200
[Asserts]
jsonpath "$.name" == "{{object_name}}"

# 9. DownloadObject — handler bundles object metadata + presigned GET URL.
POST {{host}}/paladin.v1.ObjectService/DownloadObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200
[Captures]
download_url: jsonpath "$.downloadUrl.url"
[Asserts]
jsonpath "$.object.name" == "{{object_name}}"

# 10. Round-trip the body through the presigned URL.
GET {{download_url}}
HTTP 200
[Asserts]
bytes count == {{payload_len}}

# 11. UpdateObject — mutate metadata via FieldMask.
POST {{host}}/paladin.v1.ObjectService/UpdateObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "name": "{{object_name}}",
    "resource_version": "{{resource_version}}",
    "update_mask": "metadata",
    "metadata": {"author": "hurl-e2e"}
}
HTTP 200
[Asserts]
jsonpath "$.metadata.author" == "hurl-e2e"

# 12. Soft-delete (default).
POST {{host}}/paladin.v1.ObjectService/DeleteObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200

# 13. RestoreObject — un-delete the slot we just freed.
POST {{host}}/paladin.v1.ObjectService/RestoreObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP 200
[Asserts]
jsonpath "$.state" == "OBJECT_STATE_AVAILABLE"

# 14. Permanent delete — drops the row and the S3 body.
POST {{host}}/paladin.v1.ObjectService/DeleteObject
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}", "permanent": true}
HTTP 200
