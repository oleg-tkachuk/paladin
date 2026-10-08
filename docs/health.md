# Health components

Every role reports its dependencies on `/readyz`, on
`/system/health.json` and on the console's health page. Each row is one
component, and every component follows the same contract,
`health.Probe` in `backend/internal/health`:

- **Spec** — its name, category, and whether it is critical. A critical
  failure takes the pod out of service (`/readyz` answers 503). Any other
  failure marks the role degraded, and the pod keeps serving.
- **Enablement** — its switch, read before anything else. A component that
  is off is listed as `disabled`, with the reason. Its dependency is never
  touched, and it never counts toward the role's status.
- **Check** — the call that exercises the dependency. It runs only while
  the component is on.

The switch lives in one of three places, reported as the row's `control`:

| control | turned on by | a disabled row says |
|---|---|---|
| `always_on` | nothing: the role cannot run without it | — |
| `config` | a configuration key, read at startup | `off by configuration: <key>` |
| `database` | stored rows: it is in use while something stored uses it | `not in use: <what would use it>` |

## The components

| role | component | control | critical | check |
|---|---|---|---|---|
| all but mcp | `postgres` | always_on | yes | ping the primary |
| all but mcp | `postgres-replica` | config: `datastores.postgres.replica.enabled` | no | the replica is reachable and within `max_lag` |
| api, admin | `capability` | config: `capability.enabled` | yes | the capability store answers a lookup |
| api, admin | `api_token` | config: `api_token.enabled` | no | the token store answers the lookup authentication makes |
| api | `iam_listener` | always_on | yes | the iam listener accepts a connection |
| dispatcher | `outbox` | always_on | yes | the outbox table is readable |
| dispatcher | `nats`, `rabbitmq` | config: `dispatcher.sinks.<kind>.enabled`, then database: an enabled event subscription of that sink kind | no | every broker the pool has dialed is connected |
| ingest | `subscriber` | always_on (NATS driver only) | yes | the NATS subscription is connected |
| mcp | `process` | always_on | yes | none: it reports that the process answers |

A sink kind has both switches. `dispatcher.sinks.<kind>.enabled` decides
whether the deployment delivers to the kind at all; it is on by default. Off,
the admin API refuses to create or enable a subscription of the kind, or to
send it a test delivery, and the dispatcher fails the kind's queued
deliveries, naming the key, and dials none of its brokers. Switched back on,
those deliveries can be redriven. While the kind is on, the database decides
whether it is in use.

The dispatcher's pools learn about a broker by dialing it. URL-authenticated
sinks are dialed at startup. A sink whose credentials are a Secret ref, or
that presents a client certificate, is dialed on its first delivery, so
until then its kind reads `no broker connected yet`. A failed dial is
reported, with the URL redacted, until a dial to that broker succeeds.

## Adding a component

Register a `health.Check` with `app.AddComponent`. Choose its `Switch`:

- leave it nil for `always_on`;
- use `health.Fixed(health.ByConfig(on, key))` for a configuration switch,
  with `key` a constant in `backend/internal/config/switch_keys.go`, which
  a test checks against the loader's accepted keys;
- read the database for `health.ByDatabase` when stored rows decide it.

The switch must not touch the dependency itself. That is the check's job,
and it runs only once the switch says the component is on.
