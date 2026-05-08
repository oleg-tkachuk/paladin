# TenantService — full CRUD over Connect/JSON.
#
# Variables expected (set via `--variable`):
#   host       — base URL (http://127.0.0.1:8080)
#   jwt        — bearer JWT
#   tenant_id  — UUID for the test tenant; the runner generates a fresh
#                one per invocation so reruns don't trip the unique
#                constraint.

# Create
POST {{host}}/paladin.v1.TenantService/CreateTenant
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{
    "tenant_id": "{{tenant_id}}",
    "display_name": "E2E hurl tenant",
    "labels": {"env": "e2e"}
}
HTTP 200
[Asserts]
jsonpath "$.tenantId" == "{{tenant_id}}"
jsonpath "$.displayName" == "E2E hurl tenant"
jsonpath "$.resourceVersion" exists

# Read
POST {{host}}/paladin.v1.TenantService/GetTenant
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "tenants/{{tenant_id}}"}
HTTP 200
[Asserts]
jsonpath "$.tenantId" == "{{tenant_id}}"

# List
POST {{host}}/paladin.v1.TenantService/ListTenants
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"page_size": 50}
HTTP 200
[Asserts]
jsonpath "$.tenants" exists

# Delete
POST {{host}}/paladin.v1.TenantService/DeleteTenant
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer {{jwt}}
{"name": "tenants/{{tenant_id}}"}
HTTP 200
