# Event delivery: at-least-once + the dedup contract

Paladin delivers subscription events through a transactional outbox
([ADR-0003](adr/0003-transactional-outbox.md)): the producing state change and
the outbox row commit atomically, then a background dispatcher drains the outbox
and delivers to the configured sink (HTTP, NATS, SQS, Kafka, RabbitMQ).

## The envelope: CloudEvents 1.0

Every delivery is a [CloudEvents 1.0](https://github.com/cloudevents/spec)
envelope in structured mode — the attributes and the event in one JSON body —
whichever sink carries it. The HTTP sink posts it as
`application/cloudevents+json`; the broker sinks publish the same body. An HTTP
subscription with `format: "raw"` gets the bare event JSON instead, for a
receiver written before CloudEvents became the default.

| Attribute | Value |
|---|---|
| `specversion` | `1.0` |
| `type` | the event type: `paladin.object.uploaded`, `paladin.capability.charged`, `paladin.audit.<action>` |
| `source` | `paladin` |
| `id` | the delivery id, stable across retries — the dedup key below |
| `time` | when the event happened, RFC 3339 |
| `subject` | the resource name of what it is about, as the producer names it: `capabilities/<id>`, `tenants/<id>/users/<id>`, `storageBackends/<id>` |
| `tenantid` | an extension attribute: the tenant the event belongs to |
| `datacontenttype` | `application/json` |
| `data` | the event's own fields; amounts are `google.type.Money` objects in the proto JSON mapping, `{"currency_code": "USD", "units": "1", "nanos": 500000000}` |

A subscription's CEL filter sees `type`, `tenant_id`, `resource_name`,
`actor_subject`, `kind` (the class alone: `object`, `capability`),
`severity_level` and the other envelope fields as bare names —
`type == "paladin.capability.charged"`, not `event.type`, which the server
refuses.

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
short-lived set of processed ids and drop a duplicate. A TTL slightly longer
than one row's retry span is enough: the dispatcher retries a row after
`dispatcher.base_backoff` (default 5s), doubling per attempt up to
`dispatcher.max_backoff` (default 1h), for up to the sink's `max_attempts`
(`dispatcher.default_max_attempts`, default 5, when unset) —
`OutboxRunner.backoffFor` in `internal/worker/event_dispatcher.go`.

Where to read the id, per format:

| Format | Where the id is |
|---|---|
| `cloudevents` (default) | the CloudEvents `id` field in the JSON body, **and** the `X-Paladin-Event-Id` header |
| `raw` | the `X-Paladin-Event-Id` header (the raw body is the bare event JSON and carries no CloudEvents envelope) |

Every HTTP delivery — **both** formats — also sets:

- `X-Paladin-Event-Id` — the dedup key described above.
- `X-Paladin-Event-Type` — e.g. `paladin.object.uploaded`, `paladin.audit.create_tenant`.
- `X-Paladin-Subscription-Id` — the subscription that matched.

For the broker sinks (NATS / SQS / Kafka / RabbitMQ) the payload is always the
CloudEvents envelope, so dedup on its `id` field.

## Ordering

Events are **not** ordered. Two events for the same resource can arrive out of
order (independent retries, parallel drains). If your handler cares about order,
reconcile against the resource's current state (fetch it) rather than trusting
event arrival order — the same eventual-consistency posture Paladin uses internally.
