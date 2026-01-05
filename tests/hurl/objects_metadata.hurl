# Create Object with Metadata
POST {{host}}/v1/objects
Host: paladin.local
{
  "content_type": "image/jpeg",
  "size_bytes": 1024,
  "labels": {
    "env": "test",
    "project": "acme"
  },
  "external_ref": "ref-123"
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
[Asserts]
jsonpath "$.object_key" exists
jsonpath "$.method" == "PUT"

# Get Object (Standard) - Check Labels
GET {{host}}/v1/objects/{{object_id}}
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.labels.project" == "acme"
jsonpath "$.external_ref" == "ref-123"
jsonpath "$.download_url" exists

# Get Object Meta (New Endpoint)
GET {{host}}/v1/objects/{{object_id}}/meta
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.object_id" == "{{object_id}}"
jsonpath "$.labels.env" == "test"
jsonpath "$.external_ref" == "ref-123"
jsonpath "$.download_url" not exists
jsonpath "$.status" == "pending"

# Mark Complete
POST {{host}}/v1/objects/{{object_id}}/complete
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Verify Final Meta
GET {{host}}/v1/objects/{{object_id}}/meta
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.status" == "active"
