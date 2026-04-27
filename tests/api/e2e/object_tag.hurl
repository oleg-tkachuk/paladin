# ObjectTagService — tenant-scoped taxonomy CRUD.
#
# Variables: host, jwt, tenant_id

POST {{host}}/paladin.v1.ObjectTagService/CreateObjectTag
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "slug": "e2e-tag",
    "display_name": "E2E hurl tag",
    "description": "Tag created by hurl"
}
HTTP 200
[Asserts]
jsonpath "$.slug" == "e2e-tag"

POST {{host}}/paladin.v1.ObjectTagService/GetObjectTag
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "object_tags/e2e-tag"}
HTTP 200
[Asserts]
jsonpath "$.slug" == "e2e-tag"

POST {{host}}/paladin.v1.ObjectTagService/ListObjectTags
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"page_size": 50}
HTTP 200
[Asserts]
jsonpath "$.objectTags" exists

POST {{host}}/paladin.v1.ObjectTagService/DeleteObjectTag
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "object_tags/e2e-tag"}
HTTP 200
