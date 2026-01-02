# Create Single Object
POST {{host}}/v1/objects
{
  "content_type": "image/jpeg",
  "size_bytes": 13
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_url: jsonpath "$.upload_url"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.method" == "PUT"

# Get Object Metadata
GET {{host}}/v1/objects/{{object_id}}
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.status" == "pending"

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
