## Unreleased — a tenant's audit trail holds only what was admitted to it

- An audit entry is filed in the trail of the tenant its resource names only
  when the call was admitted to that tenant: it is the caller's own, or the
  call's authorization admitted it there — a policy decision on that tenant,
  or a role that spans tenants. A call refused before that, and one that
  named a tenant it was never admitted to, is in the caller's trail alone,
  with `resource_name` recording the resource it named.
- `ListAuditLog` with a `tenant_id`, and its facet counts, no longer return
  entries of other tenants' callers that named a resource of that tenant.
- A call admitted to a tenant that then fails, a version conflict say, is
  still in that tenant's trail.
