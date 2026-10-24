# Storage event ingest — sources, wire formats & config

The ingest plane (`internal/eventingest`) settles objects Paladin has already
registered from what the storage backend reports. A storage backend fires a
notification when a key lands or goes; the ingest pod (`serve ingest`, with
`ingest.enabled: true`) consumes it, normalises it to a CloudEvents 1.0
envelope, parses the object path into `(tenant_id, collection, key)`, and:

- on an upload, runs the same PROMOTE (`PENDING` → `AVAILABLE`) the data
  plane's `CompleteObject` would have — what drives completion for a
  collection whose `completion_mode` is `IMPLICIT`;
- on a delete, HEADs the object where its row places it and moves an
  `AVAILABLE` object whose bytes are gone to the trash (see
  [deletion-semantics.md](deletion-semantics.md#deleted-from-the-bucket-not-through-paladin)).

An event for a key with no object row — a write that bypassed Paladin
entirely — is logged and skipped: ingest never creates rows. Events of a
tenant in the trash are skipped too. The worker's **Reconciler**
(`internal/worker/reconciler.go`) is the safety net — it HEADs `PENDING`
rows on a schedule regardless of events — so a missed or absent notification
for an upload made through Paladin degrades *latency*, not correctness.

---

## Architecture — ports & adapters

Two orthogonal seams. Pick one **driver** (transport) and one **source**
(wire format); they compose.

```
Driver (transport)          Source (format)
  ─ webhook   (HTTP)          ─ seaweedfs        (SF webhook JSON)
  ─ nats      (core / JS)     ─ seaweedfs_nats   (SF gob+protobuf)
  ─ rabbitmq  (AMQP)          ─ s3 / minio       (AWS S3 event JSON)
  ─ sqs       (AWS SQS)       ─ cloudevents      (CE 1.0 passthrough)
        \                    /
         ─→ Worker.Deliver ←─
                 │
                 ▼
        dedup (ingested_events) → Handler → PromoteToAvailable | SoftDelete
```

- **Driver** — where events arrive. Config: `ingest.driver` = `webhook` |
  `nats` | `rabbitmq` | `sqs`. See `IngestWebhook` / `IngestNATS` /
  `IngestRabbitMQ` / `IngestSQS` in `internal/config/types.go`.
- **Source** — how to decode the bytes. Config:
  `ingest.<nats|rabbitmq|sqs>.source_format` (the webhook driver picks per
  route instead). Selected by `pickSource` in `cmd/server/serve_ingest.go`;
  `sqs` defaults to `s3` when unset, the others require it.

### Transport × source — what actually pairs

| source_format | webhook | nats | rabbitmq | sqs | Emitted by |
|---|:---:|:---:|:---:|:---:|---|
| `seaweedfs` | ✅ `/webhook/seaweedfs` | — | — | — | SeaweedFS `[notification.webhook]` |
| `seaweedfs_nats` | — | ✅ | — | — | SeaweedFS `[notification.gocdk_pub_sub]` → NATS |
| `s3` / `minio` | ✅ `/webhook/s3`, `/webhook/minio` | ✅ | ✅ | ✅ | AWS S3, MinIO, any S3-compatible |
| `cloudevents` | ✅ `/webhook/cloudevents` | ✅ | ✅ | — | anything speaking CE 1.0 |

`nats`, `rabbitmq` and `sqs` accept any `source_format` `pickSource` knows;
the table marks the pairings a publisher actually emits.

The **`sqs`** driver is the native AWS S3 path: S3 → SQS delivers the same S3
event JSON, the driver long-polls and deletes on success (details below).

The `s3` and `minio` formats are byte-identical (MinIO mirrors the AWS
shape); they differ only in the `Source` label stamped on emitted events
(`s3://…` vs `minio://…`) for metrics/audit attribution.

---

## The canonical CloudEvent + the path contract

Every source normalises to `eventingest.CloudEvent` (`cloudevent.go`):

| field | meaning |
|---|---|
| `Type` | `paladin.object.uploaded` \| `paladin.object.deleted` |
| `Source` | e.g. `s3://primary`, `seaweedfs-nats://primary` |
| `ID` | **load-bearing for dedup** — stable across replays of the same physical event |
| `Subject` | `tenants/<t>/collections/<c>/objects-by-key/<key>` |
| `SubjectFields` | parsed `(TenantID, Collection, Key, Etag, SizeBytes, Sequencer)` |

The **object path** the parser depends on is an *observed* contract with the
backend, not one Paladin controls — a backend upgrade that changes the shape
silently stops promotions. After per-source normalisation the key is always:

```
<tenant_uuid>/<collection>/<key...>
```

- `tenant_uuid` — owning tenant (UUID). All three segments must be non-empty.
- `collection` — the Paladin collection namespace.
- `key` — the object key; **may contain `/`** (parsed with `SplitN(…, 3)`
  so the remainder is kept whole).

A path that doesn't match (a non-Paladin object dropped in the same bucket) is
**ignored** — logged, no dedup row, no error — so junk never fills the dedup
table.

---

## Per-backend matrix

| Backend | Emits notifications? | Wire format | Transport(s) | source_format |
|---|:---:|---|---|---|
| **SeaweedFS** | ✅ | gob+protobuf `filer_pb.EventNotification` **or** webhook JSON | NATS (gocdk_pubsub) / HTTP webhook | `seaweedfs_nats` / `seaweedfs` |
| **MinIO** | ✅ (native bucket notifications) | AWS S3 event JSON (`Records[]`) | webhook / AMQP / Kafka | `s3` / `minio` |
| **AWS S3** | ✅ (→ SQS/SNS/EventBridge/Lambda) | AWS S3 event JSON (`Records[]`) | SNS→HTTPS webhook / SQS (`ingest.driver: sqs`) | `s3` |
| **Garage** | ❌ **none** | — | — | *(rejected — see below)* |

---

## SeaweedFS

Two publishers; **not interchangeable** — the wrong `source_format` yields
`ErrUnrecognisedEvent` on every message.

### `seaweedfs_nats` — gocdk_pubsub over NATS

Source: `source_seaweedfs_nats.go` decodes the gob envelope wrapping a
proto-marshalled `filer_pb.EventNotification`, then `parseSeaweedFSPath`
(`source_seaweedfs.go`) parses the path.

SeaweedFS filer notification config (`notification.toml`):

```toml
[notification.gocdk_pub_sub]
enabled  = true
topic_url = "nats://seaweedfs.filer"   # publishes onto subject seaweedfs.filer
```

Paladin overlay (`ingest.nats`): `subject: seaweedfs.filer`,
`source_format: seaweedfs_nats`, `jetstream: true`,
`durable_name: paladin-ingest-sf`.

### `seaweedfs` — webhook JSON

Source: `source_seaweedfs.go`. Fields: `key`, `event_type`
(`create`/`update` → uploaded, `delete` → deleted), `timestamp_ns`, optional
`etag`/`size`/`sequencer`. Point SF's `[notification.webhook]` at
`https://…/webhook/seaweedfs`. No broker id → the adapter hashes
`(key, event_type, timestamp_ns)` for dedup.

### The `buckets/` prefix — why it's stripped

Two publishers disagree on the leading path:

| Publisher | Emitted path |
|---|---|
| SF S3-gateway **webhook** | `<bucket>/<tenant>/<collection>/<key>` |
| **gocdk_pubsub-over-NATS** | `buckets/<bucket>/<tenant>/<collection>/<key>` |

The NATS path observes the **full filer namespace**, where the S3 gateway
materialises bucket-rooted objects under `/buckets/<bucket>/…`. So
`parseSeaweedFSPath` strips a leading `buckets/` *before* the bucket-prefix
check, making both shapes parse identically. A path missing the bucket
prefix is **ignored** (wrong source / misconfig), not an error.

The bucket is not configurable today: `pickSource` hard-codes
`paladin-primary` for the `seaweedfs` / `seaweedfs_nats` sources on the
`nats` / `rabbitmq` / `sqs` drivers, and the webhook driver registers
`/webhook/seaweedfs` with **no** bucket, so that route expects a bare
`<tenant>/<collection>/<key>` path.

> ⚠️ **Drift risk.** The `buckets/` strip was inferred from observed live
> paths on the current SeaweedFS version. A future SF release that drops the
> prefix silently ignores events (no promote, no error). The Reconciler is the
> safety net — drift degrades latency, not correctness.

---

## MinIO / AWS S3 / S3-compatible — the `s3` source

Source: `source_s3.go` (`S3EventSource`). Parses the AWS S3
event-notification JSON — the canonical `Records[]` envelope S3 delivers to
SQS/SNS/Lambda/EventBridge, emitted verbatim by AWS S3, MinIO, and any
S3-compatible store that speaks bucket notifications.

```json
{
  "Records": [{
    "eventSource": "aws:s3",                       // or "minio:s3" — not checked
    "eventName":   "s3:ObjectCreated:Put",
    "eventTime":   "2026-01-02T03:04:05.678Z",
    "s3": {
      "bucket": { "name": "paladin-primary" },
      "object": {
        "key":       "<tenant_uuid>/<collection>/<key>",  // URL-encoded
        "size":      2048, "eTag": "…", "sequencer": "…"
      }
    },
    "responseElements": { "x-amz-request-id": "…" }
  }]
}
```

- **Event mapping:** `s3:ObjectCreated:*` (Put/Post/Copy/CompleteMultipart) →
  `uploaded`; `s3:ObjectRemoved:*` (Delete/DeleteMarkerCreated) → `deleted`;
  `ObjectAccessed`/lifecycle/replication → **ignored**.
- **Key decoding:** the key is **bucket-relative** (the bucket is in
  `s3.bucket.name`, so there's no bucket segment to strip — unlike SeaweedFS).
  S3 form-encodes the key: space → `+`, `/` → `%2F` (MinIO) or left literal
  (AWS), other bytes → `%XX`. The parser uses `url.QueryUnescape`, which
  handles all of it (`+`/`%20` → space, `%2F` → `/`, `%2B` → literal `+`), so
  both AWS-literal-slash and MinIO-encoded-slash keys parse identically.
- **Dedup id:** a hash of `responseElements["x-amz-request-id"]` (AWS + MinIO
  both set it, reused on retry) with `(key, eventName, sequencer)`. The
  request id alone would collapse the records of one `DeleteObjects` call,
  which share it.
- **Batching:** one CloudEvent per `Records` entry, each with its own record
  as `Data`; records Paladin does not act on are skipped. The message is
  acked only when every event was handled, and its redelivery skips the ones
  already claimed.

### MinIO config recipe

```sh
mc admin config set myminio notify_webhook:paladin \
    endpoint="https://…/webhook/minio" queue_dir=/tmp/minio-events
mc admin service restart myminio
mc event add myminio/paladin-primary arn:minio:sqs::paladin:webhook \
    --event put,delete
```

Or route MinIO → NATS/AMQP and set `ingest.driver=nats`/`rabbitmq` with
`source_format: minio`.

### AWS S3 config recipe — the `sqs` driver (native path)

The cleanest AWS path is **S3 → SQS**, polled by the `sqs` ingest driver.
Point the bucket's notification at an SQS queue, then:

```yaml
ingest:
  driver: sqs
  sqs:
    queue_url: https://sqs.us-east-1.amazonaws.com/<acct>/paladin-ingest
    region: us-east-1
    # role_arn: arn:aws:iam::<acct>:role/paladin-ingest   # cross-account (optional)
    # endpoint: http://localstack:4566                # LocalStack / tests
    max_messages: 10          # 1..10 per ReceiveMessage
    wait_time_seconds: 20     # long-poll — cuts empty receives + API cost
    visibility_timeout: 60    # hide in-flight; redrive policy DLQs after maxReceiveCount
    unwrap_sns: false         # true when the topology is S3 → SNS → SQS
    source_format: s3         # default
```

Credentials come from the ambient AWS chain (**IRSA** on EKS, env, or instance
profile); `role_arn` `sts:AssumeRole`s for a queue in another account.

**Queue lifecycle = the ack channel** (`SQSDriver`, `driver_sqs.go`):

| Outcome | Action |
|---|---|
| parse OK + deliver OK | `DeleteMessage` (ack) |
| `ErrIgnoredEvent` (not our bucket / uninteresting op) | `DeleteMessage` (ack) |
| `ErrUnrecognisedEvent` (`s3:TestEvent`, SNS control, garbage) | `DeleteMessage` (drop poison) |
| deliver error (transient — DB down) | **leave it** → reappears after `visibility_timeout`; the queue's **redrive policy** dead-letters after `maxReceiveCount` |

Set `unwrap_sns: true` for **S3 → SNS → SQS** fan-out (the driver unwraps the
SNS `Notification` envelope to reach the S3 JSON in `.Message`). For a direct
S3 → SQS subscription leave it false.

Alternatives without the SQS driver: **SNS → HTTPS subscription** pointed at
`/webhook/s3`, or S3 → **EventBridge** → API destination — the `s3` source
parses the same JSON regardless of transport.

---

## Garage — no native notifications

**Garage emits no object-lifecycle events of any kind.** `source_format:
garage` is deliberately **rejected** by `pickSource` with a directive error
rather than silently subscribing to a source that will never publish.

Authoritative (verified 2026-07-06):

- `PutBucketNotificationConfiguration` / `GetBucketNotificationConfiguration`
  are **❌ Missing** — [S3 compatibility status][garage-s3]. Missing endpoints
  return `501 Not Implemented`.
- No non-S3 event / webhook / change-feed / pub-sub mechanism exists either —
  the [feature list][garage-feat] has none (K2V is a key-value API, not a
  change feed).
- Garage's configuration has no `[notification.*]` section — there is
  nothing to configure.

**Consequence:** even when Garage is the primary (or only) backend, Paladin cannot
ingest its writes via events. An upload made through Paladin is still
completed: the worker's reconciler (`internal/worker/reconciler.go`) HEADs
`PENDING` objects on a schedule and promotes the ones whose bytes arrived. A
write that bypassed Paladin has no row and is not discovered. For event-driven
ingest of Garage-stored objects, front Garage with an S3-notification-capable
layer (e.g. SeaweedFS) and use that layer's `source_format`.

[garage-s3]: https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/
[garage-feat]: https://garagehq.deuxfleurs.fr/documentation/reference-manual/features/

---

## Dedup & delivery semantics

**Dedup.** Every event claims a row in `ingested_events`, unique on
`(source, event_id)`, before the handler runs (`Worker.Deliver`,
`worker.go`); a duplicate claim skips the handler and acks. If the handler
fails, the claim is released so the broker's redelivery is processed rather
than skipped. Sources must pick an `ID` stable across replays — a broker
message-id where available, else a deterministic content hash. Ignored events
(`ErrIgnoredEvent`) never reach `Deliver`, so they write no dedup row.

**Delivery (NATS driver).** Two modes (`ingest.nats.jetstream`):

- `false` — core pub/sub, at-most-once. A missed event on a broker restart is
  caught by the reconciler's HEAD of `PENDING` objects.
- `true` — JetStream durable consumer, at-least-once. ACK after the pipeline
  returns nil; NAK → redelivery; unparseable → term (dead-letter). Requires
  `durable_name` and a stream pre-provisioned out-of-band, named after the
  subject with dots replaced by underscores (`seaweedfs.filer` →
  `seaweedfs_filer`, `streamFromSubject`); the driver errors if it is
  absent. See `driver_nats.go` `runJetStream` and its integration
  coverage in `driver_nats_jetstream_test.go`.

**`Nats-Msg-Id` override.** When a NATS message carries `Nats-Msg-Id`, the
driver uses it as the CloudEvent `ID`, preserving dedup across upstream
re-publish. A message of several events keeps each event's own id after it
(`<msg-id>/<event-id>`), so they do not dedup as one; RabbitMQ's
`message-id` is handled the same way.
