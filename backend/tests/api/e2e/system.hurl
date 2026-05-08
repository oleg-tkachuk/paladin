# SystemService — version + config introspection over Connect/JSON.
#
# Variables expected from the runner (`hurl --variable host=... --variable jwt=...`):
#   host  — base URL of the PALADIN backend (e.g. http://127.0.0.1:8080)
#   jwt   — bearer token; SystemService is open but other RPCs need it,
#           so the runner ships one for the whole suite.

POST {{host}}/paladin.v1.SystemService/GetVersion
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{}
HTTP 200
[Asserts]
jsonpath "$.version" exists
jsonpath "$.commit"  exists

POST {{host}}/paladin.v1.SystemService/GetHealth
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{}
HTTP 200

POST {{host}}/paladin.v1.SystemService/GetConfig
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{}
HTTP 200
[Asserts]
jsonpath "$.yaml" exists
