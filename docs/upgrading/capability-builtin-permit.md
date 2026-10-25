## Unreleased — a capability does what its caveats allow without a tenant policy

- **A capability is now permitted, in its own tenant, the data-plane actions
  of the operation its caveats admitted** (ADR-0038): `get`/`list` →
  `GetObject`, `PresignGet`; `put` → `PutObject`, `PresignPut`,
  `UpdateObject`, `CopyObject`. Before, a tenant with no stored policy
  refused these with "denied by policy". A tenant that relied on that
  default to keep issued capabilities inert must add a `forbid`, for example
  `forbid (principal, action, resource) when { principal has kind && principal.kind == "capability" };`.
- **Cedar context gains `capability_op`** (String): the operation a
  capability's caveats admitted for the request, `""` without one. Tenant
  policies may read it. Additive.
