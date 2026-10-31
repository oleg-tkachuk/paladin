## v41.4.0, sdk/go/v0.131.0, sdk/python/v0.132.0 — capabilities across every tenant

- `CapabilityService.List` with no `tenant_id` lists every tenant's
  capabilities in one id order, each naming its tenant in
  `subject.tenant_id`, for `platform.admin` and `platform.capability-issuer`.
  Anyone else asking without a tenant is refused with `PermissionDenied`;
  before, a missing `tenant_id` was `InvalidArgument` for everyone.
- limes v1.0.0-rc.30 with pgstore v0.55.0, exchange v0.15.3, otelmetric
  v0.13.2 and biscuit v0.1.14.
