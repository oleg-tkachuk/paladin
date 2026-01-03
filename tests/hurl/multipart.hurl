# Initiate Multipart
POST {{host}}/v1/multipart
Host: paladin.local
{
  "content_type": "image/png",
  "size_bytes": 104857600
}
HTTP 200
[Captures]
object_id: jsonpath "$.object_id"
upload_id: jsonpath "$.upload_id"
[Asserts]
jsonpath "$.part_size" exists

# Sign Part 1
POST {{host}}/v1/multipart/{{upload_id}}/parts/1/sign
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.upload_url" exists

# Complete Multipart
POST {{host}}/v1/multipart/{{upload_id}}/complete
Host: paladin.local
{
  "parts": [
    {
      "part_number": 1,
      "etag": "dummy-etag"
    }
  ]
}
HTTP 200
[Asserts]
jsonpath "$.status" == "active"

# Abort (Cleanup test)
# Since we already completed, this might fail or be irrelevant for the same object, 
# but we can test the endpoint separately if needed.
