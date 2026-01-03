GET {{host}}/health/livez
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.status" == "alive"

GET {{host}}/health/startupz
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.status" == "started"

GET {{host}}/health/readyz
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.status" == "ready"
jsonpath "$.dependencies.postgresql" == "ok"
jsonpath "$.dependencies.seaweedfs" == "ok"
