## Unreleased — limes v1.0.0-rc.64: striped tenant aggregates, store codes

- limes migrations `042` and `043` run with the migration job: a table of
  stripes beside each tenant's budget, and revocations marked in one walk per
  statement.
- The meter counts a tenant with no ceiling in stripes instead of on its
  budget's one row, so that tenant's parallel calls stop queueing on the row
  (limes ADR-0022). It is always on. Every replica must run this release
  before one charges: an older one reads the budget without its stripes and
  checks a ceiling set meanwhile against less than was spent.
- **`capability.stripe_unlimited_tenants` is gone.** The configuration is
  strict, so a values file that still sets it stops the server from starting:
  remove the key before deploying.
- Setting a tenant's budget now waits a bounded time for that tenant's calls
  in flight. Past the bound it changes nothing and answers `UNAVAILABLE` with
  limes code `tenant_budget_busy`; send it again.
- New metric `limes_tenant_wait_seconds`, a histogram of how long each metered
  call waited for its tenant's budget row, by `limes_operation`, on the
  Operations dashboard. A wait past 100 ms is logged at WARN by
  `capability-usage`, with the tenant's id.
- A capability call whose database failed now carries limes code
  `store_unavailable` (no connection, the database shutting down or out of
  resources, a serialization failure) or `store_timeout` (a statement past
  the database's own limit) in its ErrorInfo, answered `UNAVAILABLE`: send
  it again after a pause. It carried no limes code before.
- Text that is not valid UTF-8 or holds a NUL is refused as
  `INVALID_ARGUMENT` wherever it reaches the capability library: subjects,
  notes, actors, filters, ops and external refs. It failed as a server
  error before.
