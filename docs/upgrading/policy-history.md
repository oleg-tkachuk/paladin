## v40.3.0, sdk/go/v0.124.0, sdk/python/v0.125.0 — a policy layer keeps every text it has held

- **`PolicyService.ListPolicyVersions`** pages through one layer's texts,
  newest first (`PolicyVersion`: `version`, `cedar_policy`, `written_by`,
  `written_at`). The layer is named as `storageBackends/{id}`,
  `storageBackends/{id}/buckets/{name}`, `tenants/{id}` or
  `tenants/{id}/collections/{name}`; gated as `GetEffectivePolicy` is.
  Additive.
- **Migration `101`** adds `policy_versions` and the triggers that fill it
  on every policy write, and seeds version 1 from each layer that already
  holds a policy. `written_by` is the writer's subject, absent for a write
  no request made — the seeded versions among them.
- The pool now sets `paladin.actor` on every connection it hands out; a
  tool connecting with its own pool records no writer.
