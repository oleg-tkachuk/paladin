## release/v0.4.0, sdk/python/v0.147.0, sdk/go/v0.147.0 — a tenant's slug never changes

- `TenantService.RenameTenantSlug` and `TenantService.ResolveRenamedSlug` are
  removed, from the API and from both SDKs. A slug is set when the tenant is
  created and never changes: Cedar names the tenant by it, and so do tokens,
  policies and every `tenants/<slug>` a client holds, so a rename left all of
  them pointing at a name that no longer existed. Change what people see
  through `display_name` instead.
- Migration `109` drops `tenant_slug_history`, which recorded the renames and
  is no longer read.
- A tenant restored from the trash whose slug another tenant took meanwhile
  can no longer be renamed out of the way: purge the other tenant first.
