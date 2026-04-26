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
#
# NOT YET WIRED in connectshim — covered by a sibling TODO and skipped here:
#   ObjectService.GetObject / LookupObject / DownloadObject
#   ObjectService.UpdateObject
#   ObjectService.DeleteObject  / RestoreObject / CopyObject
# Once those land, extend this flow to soft-delete → restore → purge and
# round-trip the body via DownloadObject.

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

# 6. PresignDownload — not yet hooked to a working object lookup, but the
# RPC itself is wired and should reject an unknown name with NotFound.
POST {{host}}/paladin.v1.PresignService/PresignDownload
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "{{object_name}}"}
HTTP *
