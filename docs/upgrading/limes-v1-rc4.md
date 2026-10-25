## sdk/go/v0.118.0, sdk/python/v0.119.0 — limes v1.0.0-rc.4

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
- **`paladin_capability_charges_total` is gone**: limes counts every charge
  attempt itself, as `limes_charge_decisions_total` with `limes_outcome`
  (`allowed`, `budget_exceeded`, `tenant_budget_exceeded`, …, `error`) and
  `limes_tenant_id`. The chart's `PaladinCapabilityChargesRefused` and the
  bundled dashboard read it; `capabilityChargesRefused.refusedOutcomes`
  defaults to `budget_exceeded` and `tenant_budget_exceeded`, so an override
  naming the old outcomes needs the new ones. A charge in another currency
  is `error` there, and no longer fires the alert. Point your own queries and
  alerts at the new series; `paladin_capability_charge_amount` stays.
- **The ledger's old charges can be removed**: with
  `worker.jobs.capability.purge_charges: true` the capability purger removes
  the charges taken more than `charges_older_than` ago (default and minimum
  9624h, 401 days), as limes's ledger housekeeping does. Off by default; a
  removed charge leaves every billing sum.
