GET {{host}}/version
Host: paladin-api.acme
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.service" == "paladin"
jsonpath "$.version" exists
jsonpath "$.commit" exists
