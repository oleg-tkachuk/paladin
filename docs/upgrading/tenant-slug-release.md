## sdk/python/v0.148.0, sdk/go/v0.148.0 — retired tenant slugs can be listed and released

- **`TenantService.ListRetiredTenantSlugs` and `ReleaseTenantSlug`**: list the
  slugs purged tenants held, and forget one so a new tenant can take it.
  Platform admins only. Additive.
- **`SLUG_TAKEN` errors carry ErrorInfo metadata `holder`**: `live`,
  `trashed` or `retired`. Additive.
