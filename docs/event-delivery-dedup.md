# Event delivery: at-least-once + the dedup contract

Paladin delivers subscription events through a transactional outbox
([ADR-0003](adr/0003-transactional-outbox.md)): the producing state change and
the outbox row commit atomically, then a background dispatcher drains the outbox
and delivers to the configured sink (HTTP, NATS, SQS, Kafka, RabbitMQ).

## Delivery is at-least-once

A delivery row is retried until the sink acknowledges it. Redelivery of an event
your endpoint **already processed** is normal and expected — for example:

- the sink returned `2xx` but the ack write back in Paladin failed (network blip,
  process restart) before the row flipped to `delivered`; or
- the broker publish succeeded but the confirm timed out.

In both cases Paladin re-delivers the same event. **Subscribers MUST be idempotent.**
Paladin does not offer exactly-once delivery — that guarantee cannot be built over a
network boundary without subscriber cooperation.

## Dedup on the event id

Every delivery carries a stable **event id** that is the same across every retry
of the same event (it is the outbox delivery-row id). Dedup on it: keep a
short-lived set of processed ids (a TTL slightly longer than your sink's
`max_attempts × backoff` window is enough) and drop a duplicate.

Where to read the id, per format:

| Format | Where the id is |
|---|---|
| `cloudevents` (default) | the CloudEvents `id` field in the JSON body, **and** the `X-Paladin-Event-Id` header |
| `raw` | the `X-Paladin-Event-Id` header (the raw body is the bare event JSON and carries no CloudEvents envelope) |

Every HTTP delivery — **both** formats — also sets:

- `X-Paladin-Event-Id` — the dedup key described above.
- `X-Paladin-Event-Type` — e.g. `paladin.object.available`, `paladin.audit.create_tenant`.
- `X-Paladin-Subscription-Id` — the subscription that matched.

For the broker sinks (NATS / SQS / Kafka / RabbitMQ) the payload is always the
CloudEvents envelope, so dedup on its `id` field.

## Ordering

Events are **not** ordered. Two events for the same resource can arrive out of
order (independent retries, parallel drains). If your handler cares about order,
reconcile against the resource's current state (fetch it) rather than trusting
event arrival order — the same eventual-consistency posture Paladin uses internally.
