GET {{host}}/health/livez
HTTP 200
[Asserts]
jsonpath "$.status" == "alive"

GET {{host}}/health/startupz
HTTP 200
[Asserts]
jsonpath "$.status" == "started"

GET {{host}}/health/readyz
HTTP 200
[Asserts]
jsonpath "$.status" == "ready"
jsonpath "$.dependencies.postgresql" == "ok"
jsonpath "$.dependencies.seaweedfs" == "ok"
