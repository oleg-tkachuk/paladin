# Initiate Multipart
POST {{host}}/v1/multipart
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "image/png",
  "size_bytes": 52428800,
  "labels": {
    "type": "multipart-test"
  }
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_id: jsonpath "$.upload_id"
[Asserts]
jsonpath "$.part_size" exists

# Sign Part 1
POST {{host}}/v1/multipart/{{upload_id}}/parts/1/sign
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.upload.url" exists
# Verify TTL (default 15m = 900s)
jsonpath "$.upload.url" contains "X-Amz-Expires=900"
[Captures]
part_url: jsonpath "$.upload.url"

# Upload Part 1
PUT {{part_url}}
Content-Type: image/png
base64, c21hbGwtdGVzdC1wYXlsb2Fk;
HTTP 200
[Captures]
etag_1: header "ETag" regex "\"([^\"]+)\""

# Complete Multipart
POST {{host}}/v1/multipart/{{upload_id}}/complete
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "parts": [
    {
      "part_number": 1,
      "etag": "{{etag_1}}"
    }
  ]
}
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# Verify Metadata
GET {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"
jsonpath "$.labels.type" == "multipart-test"

# Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204
