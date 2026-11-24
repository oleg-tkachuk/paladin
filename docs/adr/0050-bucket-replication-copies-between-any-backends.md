# ADR-0050: Bucket replication copies between any two backends, keeping state per object

- **Status:** Accepted 2026-11-24. Amended 2026-11-24: per-object state
  replaces the per-bucket watermark.
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

The first answer was a per-bucket watermark on `committed_at`: every object
committed at or before it had been decided. It had four faults, each one a
consequence of tracking a bucket rather than an object. One object that failed
for good held the watermark, so every later pass copied every object after it
again. Every pass read every object of the bucket's collections, whatever the
watermark. `committed_at` is stamped once, on the promote from `PENDING`, so
bytes rewritten under an `AVAILABLE` row were never copied. And the stamp is
taken when the promote's transaction starts, so a transaction that took longer
than the settle delay to commit could land behind the watermark and be passed
over.

## Decision

**The copy.** `s3adapter.ObjectRouter.Replicate` copies one object through
the official AWS SDK for Go v2. On one backend, an object within the 5 GiB
single-`CopyObject` limit copies server-side; anything else — another
backend, or a larger object — is a GET streamed into the destination's
multipart writer, which holds one part in memory, so no object is buffered
whole. Every replica is verified: a server-side copy is read back with a
HEAD, a streamed one counts the bytes written and, where the source's ETag
is a plain MD5, compares the digest. A replica that does not match is
deleted and reported as a mismatch. The replica lands at the same tenant,
collection and key in the destination bucket, with the source's stored
headers: a server-side copy keeps them (`MetadataDirective` COPY), and a
stream restates what the source's GET answered — `Content-Type`,
`Cache-Control`, `Content-Disposition`, `Content-Encoding`,
`Content-Language` and the user metadata — on the multipart create. The body
is copied as stored, so a `Content-Encoding: gzip` object stays encoded.

**The state.** One row per replica location in `object_replicas` (migration
121): the source bucket, and the tenant, collection and key the replica lands
at. The row is the work queue and the record at once.

- *What marks a row due.* Triggers on `objects`, in the writing transaction:
  an insert or update that leaves the row `AVAILABLE` in a bucket with
  replication enabled, where the update touches the state, the bytes
  (`bytes_written_at`, which every promote sets), or a field the filter reads;
  and the object's delete. A move to the trash marks nothing. Each mark bumps
  the row's `generation`. There is no timestamp window: a write is queued by
  its own transaction, which commits or does not, so no promote of any length
  can be passed over, and no clock decides what a pass sees.
- *What a pass reads.* Only the bucket's due rows, through a partial index,
  in claims of a few dozen. A claim takes `FOR UPDATE SKIP LOCKED` and a
  lease, which a later write does not shorten, so worker replicas take
  disjoint rows and one worker handles a location at a time.
- *What the worker decides.* The row names the object last written at its
  location, which is read as it is now. Gone: its replica is deleted from the
  destination, if this worker wrote one into the destination the bucket
  names now, and the row is dropped. Bound to another bucket: the row is
  dropped. In the trash: the replica stays. Outside the filter, or
  its replica already holds its current byte write: settled. Otherwise it is
  copied, and the replica recorded — destination, object, the byte write it
  copied (`bytes_written_at`, or the first commit for a row from before
  migration 111) and size.
- *What settles a row.* Only an outcome at the generation the claim read. A
  write during the copy leaves the row due, and a later pass copies it again;
  the replica can hold older bytes for a pass, never for good.
- *Where existing objects come from.* The first time the worker meets a
  bucket with replication enabled it queues every `AVAILABLE` object of the
  bucket's collections, in one statement that also records the seed
  (`replication_seeds`). A change to the bucket's replication settings drops
  that record, so a new destination receives everything and a new filter is
  applied to everything; so does a collection rebound into the bucket by a
  storage migration, whose objects no trigger queued there.
- *What counts as a byte write.* Every path that puts new bytes under a key
  — an upload, a copy, a storage event's newer write — ends in the
  promote, which sets `bytes_written_at`. A version restore repoints
  `current_version_id` only: reads serve the key's bytes, and so does the
  replica.

**Deletes.** A replica mirrors the bytes the source holds, not the object's
lifecycle state. A move to the trash leaves the source's bytes in place, so
it leaves the replica too, and a restore finds the replica current. A purge
— a permanent delete, the trash's expiry, a tenant's purge — removes the
object's row, whose delete trigger marks its location due; the worker deletes
the replica, retried by the row's backoff like a copy, and drops the row only
once the delete succeeded. The delete is a byte write on the destination, so
it waits on the same `BytesWrite` gate as a copy. A location a newer object
took over before the delete ran is that object's: the purge marks only the
rows its own object owns, and the new object's copy replaces the replica
instead.

**What a failure is.** The adapter classifies; the worker decides. Every
failure puts the one row off by its own backoff — one minute, doubling, at
most six hours — records the reason on the row, and leaves every other row
alone.

| Failure | Classified by | The worker |
|---|---|---|
| throttling, 5xx, a dropped connection, a body cut short | the SDK's standard retryables, plus the last | puts the row off and ends the bucket's tick, since its backend is likely down for every row after it; the rest of the claim is handed back untouched |
| a replica that does not match its source | `ErrReplicaMismatch` | puts the row off and carries on — the mismatch is the object's, not its backend's |
| the source's key absent from a bucket confirmed reachable | `ErrObjectNotFound`, never a bare 404 | re-reads the row and HEADs the source; settles the row only if the row is unchanged and still `AVAILABLE` and the key is still absent, and puts it off otherwise |
| a not-found the destination answered | `ErrReplicaDestinationNotFound` | puts the row off |
| anything else, and a filter that cannot evaluate on the object | — | puts the row off, and carries on with the next |

**The filter.** `replication.filter` is CEL over the same fields a
`ListObjects` filter sees, compiled once per bucket per tick through the
shared evaluator's cache. `SetReplication` refuses a filter that does not
compile. A stored one that does not — or a filter on a worker wired with no
evaluator — stops the bucket: copying everything would ignore it.

**The gate.** The worker asks `bucketgate.Check` before a bucket's claims:
`Read` on the source, from the state its listing returns, and `BytesWrite` on
the destination, from its state read by backend and bucket name. A refused
bucket is deferred — its rows wait, untouched, logged once per tick — and
taken by the first tick that admits both sides. A draining source still
replicates; a draining or provisioning destination does not. A destination
naming no registered bucket is a failure.

## Consequences

- Deployments with `worker.jobs.replication.enabled: true` copy every
  `AVAILABLE` object of a replicated bucket on the first pass, not only those
  of a lookback window; `lookback_window` is gone.
- A held object holds only itself: the others are copied once.
- An object's in-place byte write is replicated; a change to a field the
  filter reads has the object re-evaluated, and copied only if its bytes
  differ from the replica's.
- Every write of an `AVAILABLE` object in a replicated bucket costs the
  trigger a lookup of its collection's bucket and an upsert of one row; a
  write in a bucket without replication costs the lookup.
- A replica the filter stops matching stays in the destination; so does the
  replica of an object whose collection is rebound to another bucket, and
  every replica in a destination the bucket no longer names.
- The seed can miss an object only when one transaction inserts it
  `AVAILABLE` while replication is being enabled and commits after the seed
  ran; every other write is queued by its trigger once the enable commits.
- A purged object's replica is deleted; a trashed object's is kept.
- A replica carries its source's stored headers on either path. An
  operator view of the failed rows is in BACKLOG.

## Alternatives considered

- **Provider-native replication (S3 CRR).** Only within one provider and one
  account, configured outside Paladin, and blind to the filter and the gate.
- **A per-bucket watermark on `committed_at`.** What this ADR first decided;
  its four faults are in the context above.
- **Per-object state without a queue: each pass anti-joins `objects` against
  the replicated versions.** No trigger, but every pass still visits every
  object of the bucket's collections in the database, and a row it reads
  before a promote commits is decided on what it read.
- **Enqueue from the state machine's promote and delete.** The same queue,
  written by Go. Every path that changes an object's row would have to
  remember to enqueue — the promote, the purge, a restore, a tag update — and
  one that forgot would never replicate; the trigger sees them all.
- **Delete the replica on the move to the trash.** A trashed object is
  restorable, and its bytes are still in the source; deleting the replica
  would make a restore copy it again, and the replica would not survive the
  window in which a mistaken delete is most often noticed.
- **Delete replicas through `pending_purges`.** That queue holds bytes owed
  by the source, and a storage migration also records its copies there;
  neither is a replica. The replica location's own row already outlives the
  object and retries.
- **Pass over a failed object after a permanent error.** Stops the retries,
  but a configuration error — a credential, a missing bucket — reads as
  permanent and would pass every object it touched.
