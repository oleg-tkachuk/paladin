# Storage event ingest — wire-format contract

The ingest plane (`internal/eventingest`) lets objects written **directly to
the storage bucket** — bypassing PALADIN's data-plane RPCs — still get promoted to
`AVAILABLE`. A storage backend fires a notification when a key lands; the
ingest pod consumes it, parses the path into `(tenant_id, object_key, key)`,
and runs the same PROMOTE the data plane would have.

This doc pins the **path wire-format** the parser depends on, because it is an
*observed* contract with the storage backend, not one PALADIN controls. A backend
upgrade that changes the path shape will silently stop promotions if it drifts
from what's documented here.

## SeaweedFS → NATS

Source adapter: `source_seaweedfs_nats.go` (decodes the gob+protobuf filer
envelope) → `parseSeaweedFSPath` (`source_seaweedfs.go`).

### Expected path, after normalisation

```
<tenant_uuid>/<object_key>/<key...>
```

- `tenant_uuid` — the owning tenant (UUID).
- `object_key` — the PALADIN object-key namespace.
- `key` — the object key; **may contain `/`** (parsed with `SplitN(..., 3)`
  so the remainder is kept whole).

### The `buckets/` prefix — why it's stripped

Two publishers exist, and they disagree on the leading path:

| Publisher | Emitted path |
|-----------|--------------|
| SF S3-gateway **webhook** notifications | `<bucket>/<tenant>/<object_key>/<key>` |
| **gocdk_pubsub-over-NATS** (the path PALADIN runs today) | `buckets/<bucket>/<tenant>/<object_key>/<key>` |

The NATS path observes the **full filer namespace**, where the S3 gateway
materialises bucket-rooted objects under `/buckets/<bucket>/…`. So
`parseSeaweedFSPath` strips a leading `buckets/` *before* the bucket-prefix
check, making both shapes parse identically. The configured bucket
(`ingest.nats` / source config) is then stripped too; a path missing the
bucket prefix is treated as **ignored** (wrong source / misconfig), not an
error.

> ⚠️ **Drift risk.** The `buckets/` strip was inferred from observed live
> paths on the current SeaweedFS version. If a future SF release drops the
> prefix, or a different storage backend is wired to the same `ingest.driver`,
> the prefix logic must be re-checked — a wrong strip yields a non-matching
> bucket prefix and the event is silently ignored (no promote, no error). The
> data-plane Reconciler is the safety net (it re-promotes PENDING rows on its
> own schedule), so drift degrades latency, not correctness.

## Delivery semantics

The NATS binding is **core pub/sub** (`jetstream: false`) today: at-most-once.
A missed event on a broker restart is caught by the Reconciler. Production
deployments needing at-least-once should set `jetstream: true` (the
`runJetStream` branch in `driver_nats.go` already supports it) and
pre-provision the stream out-of-band — see the "Storage event ingest pipeline"
BACKLOG entry.

## Other backends

If the storage backend moves to MinIO, MinIO exposes native bucket
notifications (webhook / AMQP / Kafka). An additional source adapter mirroring
the SeaweedFS one — plus a `[bucket][notify]` config block on the MinIO side —
reuses the same `ingest.driver=nats` wiring. The path contract above still
applies; only the envelope decode changes.
