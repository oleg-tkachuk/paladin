# Storage event ingest — sources, wire formats & config

The ingest plane (`internal/eventingest`) lets objects written **directly to
the storage bucket** — bypassing Paladin's data-plane RPCs — still get promoted to
`AVAILABLE`. A storage backend fires a notification when a key lands; the
ingest pod (`serve ingest`) consumes it, normalises it to a CloudEvents 1.0
envelope, parses the object path into `(tenant_id, collection, key)`, and runs
the same PROMOTE the data plane would have.

This is a safety net for the "someone wrote straight to S3" case (a legacy
pipeline, `mc cp`, another service). Without it Paladin's DB would never learn the
object exists. The data-plane **Reconciler** is the second safety net — it
re-promotes `PENDING` rows on a schedule regardless of events — so a missed or
absent notification degrades *latency*, not correctness.

---

## Architecture — ports & adapters

Two orthogonal seams. Pick one **driver** (transport) and one **source**
(wire format); they compose.

```
Driver (transport)          Source (format)
  ─ webhook   (HTTP)          ─ seaweedfs        (SF webhook JSON)
  ─ nats      (JetStream)     ─ seaweedfs_nats   (SF gob+protobuf)
  ─ rabbitmq  (AMQP)          ─ s3 / minio       (AWS S3 event JSON)
        \                     ─ cloudevents      (CE 1.0 passthrough)
         ─→ Worker.Dispatch ←─
                 │
                 ▼
        dedup (ingested_events) → Handler → PromoteToAvailable
```

- **Driver** — where events arrive. Config: `ingest.driver` = `nats` |
  `webhook` | `rabbitmq`. See `IngestNATS` / `IngestWebhook` /
  `IngestRabbitMQ` in `internal/config/types.go`.
- **Source** — how to decode the bytes. Config: `ingest.nats.source_format`
  (or the webhook route). Selected by `pickSource` in
  `cmd/server/serve_ingest.go`.

### Transport × source — what actually pairs

| source_format | webhook | nats | rabbitmq | sqs | Emitted by |
|---|:---:|:---:|:---:|:---:|---|
| `seaweedfs` | ✅ `/webhook/seaweedfs` | — | — | — | SeaweedFS `[notification.webhook]` |
| `seaweedfs_nats` | — | ✅ | — | — | SeaweedFS `[notification.gocdk_pub_sub]` → NATS |
| `s3` / `minio` | ✅ `/webhook/s3`, `/webhook/minio` | ✅ | ✅ | ✅ | AWS S3, MinIO, any S3-compatible |
| `cloudevents` | ✅ `/webhook/cloudevents` | ✅ | ✅ | — | anything speaking CE 1.0 |

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

| Backend | Emits notifications? | Wire format | Transport(s) | source_format | Live in lab? |
|---|:---:|---|---|---|:---:|
| **SeaweedFS** | ✅ | gob+protobuf `filer_pb.EventNotification` **or** webhook JSON | NATS (gocdk_pubsub) / HTTP webhook | `seaweedfs_nats` / `seaweedfs` | ✅ (`seaweedfs_nats`, JetStream) |
| **MinIO** | ✅ (native bucket notifications) | AWS S3 event JSON (`Records[]`) | webhook / AMQP / Kafka | `s3` / `minio` | — (not deployed) |
| **AWS S3** | ✅ (→ SQS/SNS/EventBridge/Lambda) | AWS S3 event JSON (`Records[]`) | SNS→HTTPS webhook / (SQS driver: BACKLOG) | `s3` | — (not deployed) |
| **Garage** | ❌ **none** | — | — | *(rejected — see below)* | — |

---

## SeaweedFS

Two publishers; **not interchangeable** — the wrong `source_format` yields
`ErrUnrecognisedEvent` on every message.

### `seaweedfs_nats` — gocdk_pubsub over NATS (what the lab runs)

Source: `source_seaweedfs_nats.go` decodes the gob envelope wrapping a
proto-marshalled `filer_pb.EventNotification`, then `parseSeaweedFSPath`
(`source_seaweedfs.go`) parses the path.

gitops config (`deploy/manifests/storage/seaweedfs/notification-config.yaml`):

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
| **gocdk_pubsub-over-NATS** (lab) | `buckets/<bucket>/<tenant>/<collection>/<key>` |

The NATS path observes the **full filer namespace**, where the S3 gateway
materialises bucket-rooted objects under `/buckets/<bucket>/…`. So
`parseSeaweedFSPath` strips a leading `buckets/` *before* the bucket-prefix
check, making both shapes parse identically. A path missing the configured
bucket prefix is **ignored** (wrong source / misconfig), not an error.

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
- **Dedup id:** prefers `responseElements["x-amz-request-id"]` (AWS + MinIO
  both set it, reused on retry); falls back to a hash of
  `(key, eventName, sequencer)`.
- **Batching:** one CloudEvent per envelope (first `Records` entry); the rest
  stay in `Data`. Per-record fan-out is BACKLOG'd.

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
- `garage.toml` in gitops (`charts/garage/templates/configmap.yaml`) has no
  `[notification.*]` block — there is nothing to configure.

**Consequence:** even when Garage is the primary (or only) backend, Paladin cannot
ingest its writes via events. This is exactly why the lab runs the ingest
source on **SeaweedFS** (the `secondary` backend), not Garage. Direct writes to
Garage are caught by the **data-plane Reconciler** (it lists/compares on a
schedule). To get event-driven ingest for Garage-stored objects you must front
Garage with an S3-notification-capable layer (e.g. SeaweedFS) and use that
layer's `source_format`.

[garage-s3]: https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/
[garage-feat]: https://garagehq.deuxfleurs.fr/documentation/reference-manual/features/

---

## Dedup & delivery semantics

**Dedup.** Every event passes through the `ingested_events` table keyed on
`ID` before the handler runs (at-least-once safe). Sources must pick an `ID`
stable across replays — a broker message-id where available, else a
deterministic content hash. Ignored events (`ErrIgnoredEvent`) skip the dedup
write to avoid no-op rows.

**Delivery (NATS driver).** Two modes (`ingest.nats.jetstream`):

- `false` — core pub/sub, at-most-once. A missed event on a broker restart is
  caught by the Reconciler. Cheap.
- `true` — JetStream durable consumer, at-least-once. ACK after the pipeline
  returns nil; NAK → redelivery; unparseable → term (dead-letter). Requires
  the stream to be pre-provisioned out-of-band (the driver errors if it's
  absent). The lab runs this on the `seaweedfs_filer` stream with durable
  `paladin-ingest-sf`. See `driver_nats.go` `runJetStream` and its integration
  coverage in `driver_nats_jetstream_test.go`.

**`Nats-Msg-Id` override.** When a NATS message carries `Nats-Msg-Id`, the
driver uses it as the CloudEvent `ID`, preserving dedup across upstream
re-publish.
