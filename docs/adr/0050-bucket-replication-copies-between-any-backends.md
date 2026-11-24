# ADR-0050: Bucket replication copies between any two backends, behind a watermark it never passes uncopied

- **Status:** Accepted 2026-11-24.
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (the bucket gate
  and its background-worker addendum), [ADR-0006](0006-deferred-roadmap.md)
  (where a real `StorageReplicator` was deferred).

## Context

A bucket's `replication` setting (`enabled`, `destination_bucket`, a CEL
`filter`) was stored and read, and the replication worker walked the bucket's
objects, but `build_jobs.go` wired no `StorageReplicator`: the worker logged
what it would copy and copied nothing. The scope was open between same-cloud
copies only and copies between any backends; on 2026-11-24 it was decided as
any backends.

Making the copies real raised what a dry run never had to answer: when an
object may be considered replicated, what a failure does to that, what the
filter selects, and whether a backend an operator disabled or set draining is
spared.

## Decision

**The copy.** `s3adapter.ObjectRouter.Replicate` copies one object through
the official AWS SDK for Go v2. On one backend, an object within the 5 GiB
single-`CopyObject` limit copies server-side; anything else — another
backend, or a larger object — is a GET streamed into the destination's
multipart writer, which holds one part in memory, so no object is buffered
whole. Every replica is verified: a server-side copy is read back with a
HEAD, a streamed one counts the bytes written and, where the source's ETag
is a plain MD5, compares the digest. A replica that does not match is
deleted and reported as a transient mismatch. The replica lands at the same
tenant, collection and key in the destination bucket.

**What a failure is.** The adapter classifies; the worker decides.

| Failure | Classified by | The worker |
|---|---|---|
| throttling, 5xx, a dropped connection, a body cut short, a mismatch | the SDK's standard retryables, plus the last two | retries with exponential backoff (1s doubling, at most 30s, 4 tries); still failing, stops the bucket for the tick |
| the source's key absent from a bucket confirmed reachable | `ErrObjectNotFound`, never a bare 404 | re-reads the row and HEADs the source; passes over the object only if the row is unchanged and still `AVAILABLE` and the key is still absent |
| a not-found the destination answered | `ErrReplicaDestinationNotFound` | holds |
| anything else, and a filter that cannot evaluate on the object | — | holds, and carries on with the next object |

**The watermark.** Per bucket, every object committed at or before it has
been decided — copied, filtered out, or confirmed gone. A pass selects the
`AVAILABLE` objects committed after the watermark and at or before the
horizon, the database's clock less a settle delay of one minute. It advances
the watermark to the latest `committed_at` it decided, and only after a walk
that completed with no object held. It never advances to the horizon: a row
whose `committed_at` (stamped when its promote's transaction starts) is older
than the horizon but which becomes visible after the pass is still after the
watermark. The clock is the database's, which stamps `committed_at`, so pod
clock skew does not move the horizon. The settle delay bounds the one case
left: a promote transaction that takes longer than the delay to commit, with
a stamp older than objects the pass already decided.

A watermark the store cannot return defers the bucket rather than falling
back to the lookback window, which could start after objects never copied.

**The filter.** `replication.filter` is CEL over the same fields a
`ListObjects` filter sees, compiled once per bucket per tick through the
shared evaluator's cache. `SetReplication` refuses a filter that does not
compile. A stored one that does not — or a filter on a worker wired with no
evaluator — stops the bucket: copying everything would ignore it.

**The gate.** The worker asks `bucketgate.Check` before a bucket's walk:
`Read` on the source, from the state its listing returns, and `BytesWrite` on
the destination, from its state read by backend and bucket name. A refused
bucket is deferred — nothing copied, the watermark unchanged, logged once per
tick — and taken by the first tick that admits both sides. A draining source
still replicates; a draining or provisioning destination does not. A
destination naming no registered bucket is a failure.

## Consequences

- Deployments with `worker.jobs.replication.enabled: true` start copying on
  this release. Objects committed before the lookback window of the first
  pass are not copied.
- A held object holds its bucket's watermark, and every pass re-selects the
  objects after it; those copy again, overwriting each replica with the same
  bytes, until the held one is decided.
- Each pass walks every object of every collection bound to the bucket; the
  watermark limits what is copied, not what is read.
- Deletes are not replicated, and a streamed replica carries the source's
  content type but not its other metadata.

The last three are in BACKLOG.

## Alternatives considered

- **Provider-native replication (S3 CRR).** Only within one provider and one
  account, configured outside Paladin, and blind to the filter and the gate.
- **Advance to the horizon after a clean pass.** Simpler, and it moves the
  watermark over empty intervals, but it passes a row that becomes visible
  late with an older stamp.
- **Pass over a failed object after a permanent error.** Stops the re-copies,
  but a configuration error — a credential, a missing bucket — reads as
  permanent and would pass every object it touched.
