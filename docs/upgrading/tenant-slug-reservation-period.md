## Unreleased — a purged tenant's slug can be released after a period

- **`admin.tenants.slug_reservation_period`** (optional): how long a purged
  tenant's slug stays reserved. Set, the worker releases each slug once that
  long has passed since its purge and logs it. Unset — the default — keeps
  every slug until an operator releases it, as before. It must be positive and
  no shorter than `auth.access_token_ttl` and `auth.scoped_token_max_ttl`, or
  the pods refuse to start.
- **A slug purged again after reuse is retired anew.** With
  `admin.tenants.reserve_slugs: false`, a slug taken again and purged again
  now records the tenant purged last and that purge's time; it used to keep
  the first tenant and the first purge.
