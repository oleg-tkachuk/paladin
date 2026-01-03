GET {{host}}/version
Host: paladin.local
HTTP 200
[Asserts]
jsonpath "$.service" == "paladin"
jsonpath "$.version" exists
jsonpath "$.commit" exists
