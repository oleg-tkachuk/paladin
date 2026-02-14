GET {{host}}/version
Host: api.paladin.local
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.service" == "paladin"
jsonpath "$.version" exists
jsonpath "$.commit" exists
