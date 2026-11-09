## Unreleased — rate limits count off the request path

- The tenant and token rate limits no longer write Postgres on every request
  ([ADR-0046](../adr/0046-rate-limits-off-the-request-path.md)). With the
  default `middleware.rate_limit.backend: postgres`, each replica counts in
  memory and shares its counts every `middleware.rate_limit.sync_interval`
  (`1s`). A tenant can exceed its ceiling by at most
  `(replicas − 1) × rate × sync_interval` — about 3% of a minute's budget at
  three replicas.
- `backend: valkey` counts exactly in a Valkey given under `datastores.valkey`
  (`addr`, `password_secret`, `tls`, `timeout`). Add the password Secret's name
  to `rbac.secretReader.secretNames`.
- When the store does not answer, each replica now enforces the full rate on
  its own instead of admitting every request. `PaladinRateLimitFailingOpen`
  also fires on that, from `paladin_ratelimit_degraded_total`.
- Migration `108` replaces `tenant_rate_buckets` and `api_token_rate_buckets`
  with one UNLOGGED `rate_limit_buckets`; the counts of the minute it runs in
  are dropped. The worker job `tenant_rate_bucket_sweeper` is now
  `rate_limit_bucket_sweeper`.
