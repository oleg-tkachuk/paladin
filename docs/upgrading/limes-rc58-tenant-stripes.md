## Unreleased — limes v1.0.0-rc.58: striped tenant aggregates

- limes migrations `042` and `043` run with the migration job: a table of
  stripes beside each tenant's budget, and revocations marked in one walk per
  statement.
- `capability.stripe_unlimited_tenants` (default `false`) has the meter count
  a tenant with no ceiling in stripes instead of on its budget's one row, so
  that tenant's parallel calls stop queueing on the row (limes ADR-0022).
  Turn it on only once every replica runs this release: an older one reads
  the budget without its stripes and checks a ceiling set meanwhile against
  less than was spent. Turning it off again is safe.
- Setting a tenant's budget now waits a bounded time for that tenant's calls
  in flight. Past the bound it changes nothing and answers `UNAVAILABLE` with
  limes code `tenant_budget_busy`; send it again.
- New metric `limes_tenant_wait_seconds`, a histogram of how long each metered
  call waited for its tenant's budget row, by `limes_operation` and
  `limes_tenant_id`, on the Operations dashboard. A wait past 100 ms is logged
  at WARN by `capability-usage`.
