# Test API headers implementation
# Verify X-API-Version and X-RateLimit-* headers are present

GET {{host}}/version
HTTP 200
[Asserts]
header "X-API-Version" exists
header "X-RateLimit-Limit" exists
header "X-RateLimit-Remaining" exists

# Test headers on API endpoint
GET {{host}}/v1/objects
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
header "X-API-Version" exists
header "X-RateLimit-Limit" == "1000"
header "X-RateLimit-Remaining" == "999"
