GET {{host}}/version
Host: paladin.paladin.svc.cluster.local
HTTP 200
[Asserts]
jsonpath "$.service" == "paladin"
jsonpath "$.version" exists
jsonpath "$.commit" exists
