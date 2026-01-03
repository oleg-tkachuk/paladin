# Upload PDF File Test
# Tests creating a PDF object, getting metadata, marking it complete, and deleting it

# Create Single PDF Object
POST {{host}}/v1/objects
{
  "content_type": "application/pdf",
  "size_bytes": 12345
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload_url"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.method" == "PUT"
jsonpath "$.upload_url" exists

# Get Object Metadata
GET {{host}}/v1/objects/{{object_id}}
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "pending"
jsonpath "$.content_type" == "application/pdf"
jsonpath "$.size_bytes" == 12345

# Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Verify Final State
GET {{host}}/v1/objects/{{object_id}}
HTTP 200
[Asserts]
jsonpath "$.status" == "active"
jsonpath "$.content_type" == "application/pdf"

# Delete Object
DELETE {{host}}/v1/objects/{{object_id}}
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"

# Verify Object is Deleted
GET {{host}}/v1/objects/{{object_id}}
HTTP 200
[Asserts]
jsonpath "$.status" == "deleted"
