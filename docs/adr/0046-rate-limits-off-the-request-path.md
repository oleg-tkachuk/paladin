# ADR-0046: Request rate limits count in Valkey when one is configured, and in memory synced to Postgres otherwise

- **Status:** Accepted 2026-11-09.
- **Related:** [ADR-0044](0044-event-fan-out-in-the-dispatcher.md)
  (the other write taken off the request path).

## Context

Two limiters bound request rates across replicas: the per-tenant limit
(`middleware.rate_limit`) and the per-token limit (`api_tokens.rate_limit_rpm`).
Both count in Postgres, in one-minute buckets weighted into a trailing
minute (`tenant_rate_buckets`, `api_token_rate_buckets`). They moved there
from memory because an in-process bucket gave each replica its own ceiling,
so the configured rate was multiplied by the replica count.

The price is a write per request: every authenticated call upserts the
tenant's current bucket, and a call with a capped token upserts the token's
too. Both rows are hot — one per tenant or token per minute — and both writes
land on the database the limit is meant to protect. When that database is
slow or unreachable, both limiters admit every request (`fail_open`), so
the limit is lifted exactly when the load is highest.

## Decision

1. **One knob chooses where both limiters count:**
   `middleware.rate_limit.backend: "postgres" | "valkey"`, default `postgres`.
   One setting for both, so the tenant and token limits cannot disagree about
   where a request was counted.
2. **`valkey`: exact and shared, without Postgres.** Connection settings live
   in `datastores.valkey` (`addr`, `password` / `password_secret`, `tls`),
   beside Postgres. Each tenant or token is one hash holding its two minute
   buckets. One Lua script reads the server's clock (`TIME`), bumps the
   current bucket and returns both counts, so every replica counts on one
   clock and one key, as the Postgres statement does with `NOW()`. The
   tenant script counts every request; the token script counts only an
   admitted one, as today. The key expires two windows after its last use,
   so nothing sweeps it. The official client, `valkey-go`, is imported by one
   package, `internal/ratelimit/valkey`.
3. **When Valkey cannot answer, the limit stays on in the process.** A failed
   call is decided by a per-process window at the configured rate instead of
   being admitted. Each replica then grants the full rate, so the ceiling is
   the replica count times the setting until Valkey answers again; that is
   looser than the shared limit and far tighter than none. Each change between
   shared and per-process counting is logged once and counted on
   `paladin.ratelimit.degraded`.
4. **`postgres`: counted in memory, synced each second.** Each replica counts
   its own requests and, every `sync_interval` (default `1s`), adds what it
   counted to the shared buckets and reads the totals back. A request is
   decided against the last totals read plus what the replica counted since.
   Postgres takes one write per replica per interval instead of one per
   request. The cost is a bounded overshoot: replicas do not see each other's
   requests for up to one interval, so a tenant can exceed its ceiling by at
   most `(replicas − 1) × rate × sync_interval` — about 3% of a minute's
   budget at three replicas and `1s`. A failed sync leaves the replica on its
   own counts, which is the per-process fallback of decision 3.
5. **The login and OAuth token limiters stay per process.** They are in
   memory today, so their ceiling is already multiplied by the replica count.
   Moving them onto the same backend is recorded in BACKLOG.

## Consequences

- With Valkey, the limits are exact again and cost Postgres nothing; Valkey
  becomes one more stateful component, which is why it is optional.
- Without Valkey, Postgres loses its per-request writes in exchange for an
  overshoot bounded by the sync interval.
- An outage of either store degrades the limit instead of lifting it.
- `fail_open` stays the signal for a limiter that admitted a request it could
  not decide; with the per-process fallback it should stay at zero.
