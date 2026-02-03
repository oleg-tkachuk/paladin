# File Lifecycle Test
# Covers: Create -> Upload (File) -> Complete -> Retrieve -> Meta (List) -> Delete

# 1. Create Object Entry
POST {{host}}/v1/objects
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 96
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload.url"
[Asserts]

# 2. Upload File Content to Presigned URL
PUT {{upload_url}}
Content-Type: text/plain
x-amz-server-side-encryption: AES256
file,fixtures/hello.txt;
HTTP 200

# 3. Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "complete"

# 4. Retrieve (Sign Download URL)
POST {{host}}/v1/objects/{{object_id}}/sign-download
Host: paladin-api.acme
X-Tenant-ID: test-tenant
{}
HTTP 200
[Asserts]
jsonpath "$.download.url" exists
[Captures]
download_url: jsonpath "$.download.url"

# 5. Verify Content (Download)
GET {{download_url}}
HTTP 200
[Asserts]
bytes == file,fixtures/hello.txt;

# 6. List This File (Get Metadata)
GET {{host}}/v1/objects/{{object_id}}/meta
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "complete"
jsonpath "$.content_type" == "text/plain"
jsonpath "$.size_bytes" == 96

# 7. Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin-api.acme
X-Tenant-ID: test-tenant
HTTP 204
