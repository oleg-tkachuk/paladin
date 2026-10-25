## Unreleased — limes v1.0.0-rc.4

- **`CapabilityServiceRevokeBiscuitResponse` gains `already_revoked` and
  `revocation`**, as the capability's own revoke answers: a second revoke of
  a copy keeps the first entry and says so. Additive.
- **The capability metrics limes exports are renamed** (otelmetric
  v0.8–v0.9): the attributes `outcome`, `unit_code` and `tenant_id` are
  `limes.outcome`, `limes.unit_code` and `limes.tenant_id`; the revocation
  cache's `kind` is `limes.revocation_cache.kind`, `…clears_revoked` is
  `…narrow_clears` and `…last_sync_loss` is `…last_sync_loss_time`. Update
  queries, dashboards and alerts that read them.
- **`capability.revocation_cache_ttl` or `revocation_cache_synced_ttl`
  above 24 hours is refused at startup**: limes now caps how long its
  revocation cache trusts an answer, and caches for the longer of the two.
- **A capability listing's page token is bound to its tenant**: one handed
  back for another tenant is refused. A token issued before the upgrade is
  refused once; start again from the first page.
