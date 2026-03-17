GET {{host}}/health/livez
Host: api.paladin.local
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.status" == "alive"

GET {{host}}/health/startupz
Host: api.paladin.local
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.status" == "started"

GET {{host}}/health/readyz
Host: api.paladin.local
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.status" == "ready"
jsonpath "$.dependencies.postgresql" exists
jsonpath "$.dependencies.seaweedfs" exists
