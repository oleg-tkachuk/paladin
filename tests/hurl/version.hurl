GET {{host}}/version
Host: paladin.paladin.svc.cluster.local
X-Tenant-ID: default
HTTP 200
[Asserts]
jsonpath "$.service" == "paladin"
jsonpath "$.version" exists
jsonpath "$.commit" exists
