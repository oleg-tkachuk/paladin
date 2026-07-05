# JetStream event bus

PALADIN can publish its events onto a NATS **JetStream** stream so downstream
services (analytics, a search index, other agents) consume them durably, fan out
independently, and **replay** from any point — without PALADIN knowing who the
consumers are or widening the SQL outbox.

This builds on the transactional outbox
([ADR-0003](adr/0003-transactional-outbox.md)) and the at-least-once delivery
contract ([event-delivery-dedup.md](event-delivery-dedup.md)): JetStream is one
more sink the outbox drains into, distinguished by durability + replay.

## The stream

A single stream carries every PALADIN event:

| Field | Value |
|---|---|
| Name | `PALADIN_EVENTS` |
| Subjects | `paladin.events.>` |
| Storage | file |
| Retention | limits (age/size caps; not interest — consumers come and go) |
| Duplicate window | ≥ the outbox's max redelivery span (e.g. `2m`) |

It is provisioned declaratively by gitops (a one-shot idempotent `nats stream
add` Job alongside the NATS app), not by PALADIN at boot — PALADIN only *publishes*.

## Producing (an PALADIN subscription → the bus)

Create an event subscription with a **NATS sink in JetStream mode**. The
`subject` must fall under the stream's `paladin.events.>` filter:

```jsonc
{
  "parent": "tenants/<tenant-uuid>",
  "subscription": {
    "filter": "type.startsWith('paladin.object.')",   // optional CEL
    "sink": {
      "nats": {
        "url": "nats://nats.nats.svc.cluster.local:4222",
        "subject": "paladin.events.<tenant-uuid>.object",
        "jetstream": true
      }
    }
  }
}
```

In JetStream mode the dispatcher publishes each CloudEvents envelope
**synchronously** and sets `Nats-Msg-Id` to the CloudEvents `id` (the
retry-stable delivery-row id). The server persists it and **dedups** an
at-least-once redelivery within the stream's duplicate window — so the outbox's
at-least-once delivery becomes an effectively-once publish onto the bus.

Subject convention: `paladin.events.<tenant_id>.<class>` lets consumers wildcard by
tenant (`paladin.events.<id>.>`) or by class across tenants (`paladin.events.*.object`).

## Consuming

Each downstream service creates its **own durable consumer** — every consumer
gets the whole stream (fan-out), independent cursors:

```bash
# a service that wants every object event, from now on
nats consumer add PALADIN_EVENTS search-indexer \
  --filter 'paladin.events.*.object' --ack explicit --deliver new --pull
```

An HTTP-sink subscription stays a first-class consumer of PALADIN events too — it
just consumes via the outbox → HTTP path instead of the stream. JetStream is
additive: it does not replace the per-subscription sinks.

## Replay (rebuild a sink from sequence N)

Because the stream retains messages, a consumer can be (re)built to re-read from
any point — the "replay tooling" is native NATS:

```bash
# from a specific stream sequence
nats consumer add PALADIN_EVENTS rebuild --start-seq 4200 --deliver by_start_sequence --pull
# or from a wall-clock time
nats consumer add PALADIN_EVENTS rebuild --start-time '2026-07-01T00:00:00Z' --deliver by_start_time --pull
# or the whole retained history
nats consumer add PALADIN_EVENTS rebuild --deliver all --pull

# then drain it
nats consumer next PALADIN_EVENTS rebuild --count 1000
```

To rebuild a downstream sink after a bug or a schema change: delete its consumer,
recreate it with `--start-seq`/`--start-time` at the point you need, and let it
re-process. Idempotency on the CloudEvents `id` (see the dedup doc) makes the
replay safe.

## Retention & sizing

`PALADIN_EVENTS` uses **limits** retention (drop oldest past an age/size cap), not
interest retention — consumers are external and transient, so the stream must
not depend on any of them acking. Size the age cap to the longest replay window
you want to support; monitor with the `paladin.outbox.pending` gauge (producer-side
backlog) and `nats stream info PALADIN_EVENTS` (stored bytes / message count).
