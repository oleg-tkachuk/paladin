## v40.3.0, sdk/go/v0.125.0, sdk/python/v0.126.0 — a policy version names its writer's tenant

- **`PolicyVersion.written_by_tenant_id`**: the writer's own tenant, in which
  `written_by` names a user — a platform admin editing another tenant's
  policy is recorded under the platform tenant. Absent with `written_by`, and
  for versions written before it was recorded. Additive.
- **Migration `102`** adds the column and records it; the pool sets
  `paladin.actor_tenant` beside `paladin.actor`.
