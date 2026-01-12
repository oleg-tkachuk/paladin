# File Lifecycle Test
# Covers: Create -> Upload (File) -> Complete -> Retrieve -> Meta (List) -> Delete

# 1. Create Object Entry
POST {{host}}/v1/objects
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
{
  "content_type": "text/plain",
  "size_bytes": 96
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload_url"
[Asserts]

# 2. Upload File Content to Presigned URL
PUT {{upload_url}}
Content-Type: text/plain
file,fixtures/hello.txt;
HTTP 200

# 3. Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# 4. Retrieve (Get Download URL)
GET {{host}}/v1/objects/{{object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.download_url" exists
[Captures]
download_url: jsonpath "$.download_url"

# 5. Verify Content (Download)
GET {{download_url}}
HTTP 200
[Asserts]
bytes == file,fixtures/hello.txt;

# 6. List This File (Get Metadata)
GET {{host}}/v1/objects/{{object_id}}/meta
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "active"
jsonpath "$.content_type" == "text/plain"
jsonpath "$.size_bytes" == 96

# 7. Cleanup
DELETE {{host}}/v1/objects/{{object_id}}
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: test-tenant
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"
