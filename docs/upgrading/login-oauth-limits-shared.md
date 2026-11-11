## release/v0.2.0 — login and OAuth limits shared across replicas

- The login limits (`auth.login_rate_limit_per_subject_per_minute`,
  `auth.login_rate_limit_per_ip_per_minute`) and the `/oauth/token` limits
  (`auth.oauth.token_rate_limit_per_minute`,
  `auth.oauth.token_exchange_rate_limit_per_minute`) now count where
  `middleware.rate_limit.backend` names, like the tenant and token limits
  ([ADR-0046](../adr/0046-rate-limits-off-the-request-path.md)). Each replica
  used to grant the full rate on its own, so the cap was the replica count
  times the setting; it is now the setting.
- The OAuth limit is a sliding one-minute window rather than a token bucket:
  the same number per minute, with no burst carried over from an idle minute.
- A refused login now carries `Retry-After`. A limit the store cannot decide
  refuses the call: a login with `RESOURCE_EXHAUSTED`, a token request with
  `503 temporarily_unavailable`.
- Only the api and admin planes build the rate counter now; the worker,
  dispatcher and ingest no longer connect to Valkey, and the chart lets only
  api and admin out to it.
