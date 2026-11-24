# ADR-0051: An object records the bucket its bytes are in, and a storage migration moves objects one by one

- **Status:** Proposed 2026-11-24 — implementation in three stages (see
  *Stages*). Supersedes the storage-migration parts of
  [ADR-0015](0015-per-tenant-bucket-layout.md): Phase 3 and the migration
  addenda of 2026-11-24.
- **Related:** [ADR-0015](0015-per-tenant-bucket-layout.md) (the layouts, the
  bucket gate), [ADR-0003](0003-transactional-outbox.md) (a promote and its
  event on one transaction; purge debts), [ADR-0050](0050-bucket-replication-copies-between-any-backends.md)
  (replication reads the source's bytes).

## Context

Where an object's bytes are is derived today from its collection's binding,
`collections.bucket_id`: one mutable pointer for every object of the
collection. ADR-0015's storage migration copies a tenant's objects into the
target and then flips that pointer for all of them at one instant, the
rebind (and flips it back in a rollback).

Every write that is in flight across that instant lands on one side of it,
and the read path cannot tell which. Each mechanism added since exists to
decide that, and each one left a narrower window behind it:

- the promote fence: `statemachine.checkPlacement`, the migration row held
  `FOR SHARE` in `migstate.FencingPromotes`, and `ErrBindingMoved`;
- the start lock and the bindings lock (`migstate.LockSpaceStart`,
  `LockSpaceBindings`), with `ErrBucketMigratedAway` for a collection created
  across a rebind;
- the copy's start stamp and the catch-up before the rebind
  (`copy_started_at`, `storage_migration_caught_up`, `ErrCopyBehind`);
- the rollback's copy back and its record (`rebound_at`,
  `storage_migration_copied_back`, `ErrRollbackBehind`, the dead
  `rollback_cursor_at` / `rollback_cursor_id`);
- the late-write moves after the rebind and after the rebind back
  (`PromoteLateSourceWriteInTx`, `rebound_back_at`,
  `statemachine.LateWriteClockMargin`);
- the cleanup's record of what each copy took (`storage_migration_source_etags`,
  `ErrCleanupKept`) and the holds on it;
- the relocation of an upload or an assembled multipart object into the
  binding at completion (`objecth.uploadedBytes`, `multiparth.settle`).

For a presigned upload in explicit mode the only witness of when its bytes
landed is a storage event, which a transport may drop, delay or deliver out
of order, and which some sources send without a sequencer. Four open BACKLOG
entries are what is left of the windows; each fix so far opened another.

The data already bounds the problem:

- Bytes reach a bucket only through a URL Paladin signed or a copy Paladin
  made. Every `UploadObject` inserts a `PENDING` row naming the bucket its URL
  is signed for (`objects.upload_bucket_id`, migration 113) and when the URL
  expires (`presign_expires_at`).
- A URL's lifetime is capped by `limits.presign.max_ttl` (at most seven days,
  SigV4's limit), and a part URL by the same cap; a multipart session lives
  until the reaper's TTL at most.
- `objects` is unique on `(collection_id, path)` in every state,
  `UploadObject` refuses a taken path, and `RegenerateUploadURL` serves only a
  `PENDING` row. So Paladin never hands out a second URL for a key that has a
  row: an `AVAILABLE` object's bytes change only through the one URL its row
  was registered with.
- That URL signs the size and the checksum (`ContentLength` on a PUT, a
  `content-length-range` of one size on a POST, the `x-amz-checksum-*`
  header on both). On a store that enforces the binding, a second PUT
  through it writes the same bytes.
- Versions keep no bytes of their own: `object_versions` rows share the
  object's key, `RestoreObjectVersion` repoints `current_version_id`, and no
  S3 `VersionId` is used. Bytes are per object.

So the set of URLs that can still write to a bucket is known and finite, and
a write that lands in a bucket can be attributed to the row whose URL named
that bucket. What is missing is a record, per object, of where its bytes are.

## Decision

### An object records its placement

`objects.placement_bucket_id` (references `buckets`, `ON DELETE RESTRICT`) is
the bucket the object's current bytes are in. It is `NULL` only on a
`PENDING` row whose bytes have not been confirmed anywhere.

- **A promote sets it** to the bucket the bytes were confirmed in: the
  bucket the row's URL is signed for (`upload_bucket_id`), which is where a
  `CompleteObject` HEAD, a multipart assembly, the reconciler and a storage
  event find them; for `CopyObject` and a batch copy, the destination the
  copy wrote, which is the row's anchor too. Nothing else is accepted: a
  promote naming any other bucket is refused, so bytes found where no URL of
  the row points are never attributed to it.
- **Every path that touches existing bytes resolves them by placement**: a
  download and a presigned GET, a copy's source, the HEAD a restore makes, a
  permanent delete and its purge debt, the lifecycle hard delete, the
  replication worker's source, the placement reported to a reader, and a
  storage delete event, which acts only on the bucket the object is placed in.
- **The binding keeps every logical attribute.** `collections.bucket_id` (and
  the tenant's default binding) decides where a new upload, a multipart
  initiate and a copy's destination are signed, and it stays the bucket whose
  settings apply to the collection: its constraints, versioning and lock
  defaults, completion mode, public flag, quota and the Cedar resource's
  bucket. The bucket gate admits a read by the state of the placement bucket
  and a new write by the state of the binding.

An operator's `BindCollectionToBucket` then moves no bytes and breaks no
read: objects keep reading from where they are, and new uploads go to the
new bucket.

### A URL's bucket is kept until it can no longer write

The anchor (`upload_bucket_id`) is no longer cleared by the promote. It names
the one bucket a URL of the row can write to, until the reconciler **settles**
it: once `presign_expires_at` is past by the reconciler's grace
(`worker.jobs.reconciler.min_object_age`, which already covers a PUT that
started before its URL expired), it HEADs the anchor.

- `PENDING`: as today — the bytes are promoted (placement = anchor) or the row
  is discarded.
- `AVAILABLE` or in the trash, the bucket holding the ETag the row records:
  the anchor is cleared.
- A different ETag: the URL wrote again after the last promote, and no event
  recorded it (a store that did not enforce the URL's binding, or an event
  lost). The bytes found are recorded as a promote would record them, the
  content version moves, `paladin.object.uploaded` is enqueued on the same
  transaction, and the anchor is cleared; logged at Warn.
- No bytes: logged at Error and the anchor cleared; the row is left as it is
  (a storage delete event is what trashes a row whose bytes went).

`RegenerateUploadURL` signs for the row's anchor, never for the binding, so a
row has URLs in one bucket only. A `CopyObject` or batch-copy destination row
is anchored to the destination at creation, which the copy writes.

A storage event is a hint about one bucket. It is applied to a row only when
its bucket is the row's unsettled anchor — the one bucket a URL of the row
can still write — and its sequencer is then compared with the row's, which
came from that same bucket. An event from any other bucket (a migration's own
copy, a URL of a deleted row writing the same key, a late event for a write
already settled) is skipped and logged. A lost event costs latency, never
data: the settle finds the write.

**The rule for two writes to one key:** the last promoted wins. A row records
the bytes of the last promote of its own anchor, by a `CompleteObject`, an
event, the reconciler or the settle, in commit order. Bytes a URL of a deleted
row writes under the same key belong to no row and are never promoted.

### A migration moves objects, not a binding

A migration covers the tenant's private collections (public ones stay where
they are, as before). It is a sequence of per-object moves with no instant
that a write can fall on the wrong side of.

- **`provisioning`** waits for the target bucket, as today. Nothing else
  changes; a failed provisioning fails the migration.
- **Entering `moving`**, on one transaction: every private collection bound
  to the source and the tenant's default binding are pointed at the target,
  `storage_layout` becomes `dedicated`, and the migration records the
  source's **write deadline**, now plus `limits.presign.max_ttl`. From here
  new uploads are signed for the target; the source receives writes only
  through URLs signed before. Each pass re-points any private collection
  still bound to the source — one created from a binding read before the
  switch — so no lock is shared with `CreateCollection`.
- **No URL for the source outlives the deadline.** A regenerated PUT URL or a
  part URL for a row or session anchored to a migration's source is signed to
  expire by the deadline at the latest, and is refused past it. So a client
  that keeps regenerating cannot hold the migration open.
- **A move** takes one object placed in the source whose anchor is settled
  (`upload_bucket_id IS NULL`): no URL can write its key there. Holding the
  row `FOR KEY SHARE` (a purge waits; a metadata change does not), it reads
  the row's size, ETag and content version, checks that the source holds the
  recorded ETag, copies the bytes to the target under the same key and
  verifies the copy: the size always, and the ETag where both buckets report
  a plain MD5 (a same-backend single-part copy). Then, on the same
  transaction, it sets the placement to the target and the row's ETag to the
  target's, only where the placement is still the source and the content
  version is still the one read. A row that changed is left for the next
  pass, and the copy is overwritten by the next move. A trashed object whose
  bytes are already gone from the source moves without a copy.
- **A late write is not special.** A URL signed before `moving` may still
  write the source; the row's promote (or the settle) places the object in
  the source, and a later pass moves it once its anchor settles.
- **`completed`** is a data condition, checked each pass: no row of the
  tenant placed in the source, no row anchored to it (every URL signed for it
  has expired and been settled), no multipart session anchored to it, no
  private collection nor default binding bound to it, and the write deadline
  plus the reconciler's grace passed. The source copies are kept until
  `cleanup_after`: the later of the retention the operator asked for and
  `max_ttl` after completion, so a presigned GET signed for the source before
  its object moved keeps working until it expires.
- **`cleaned`**: the cleanup walks the tenant's private objects placed in the
  target and deletes each one's key from the source. Deleting an absent key
  succeeds, so the walk is idempotent and resumable by its cursor.

`objects_total` reports the objects moved plus those still placed in the
source, recounted each pass; `objects_copied` reports the moves.

### A rollback is the same move in reverse

The migration enters `rolling_back` when the target bucket leaves `ready`, or
when moves have failed their verification on `migstate.MaxFailedPasses`
passes in a row (the last failure is recorded as the reason). Entering it
points the collections and the default binding back at the source, sets
`storage_layout` to `shared` and records the target's write deadline. The
passes then move every object placed in the target to the source, under the
same rules with the buckets swapped, and the migration becomes `failed` when
the same data condition holds for the target. The target's copies are left
in the tenant's bucket; a retry overwrites them. A `failed` migration is
retried with `MigrateTenantStorageLayout`, as today.

### What a purge owes

A purge owes the bucket the object is placed in, and — while the tenant has a
migration not yet `cleaned` — the migration's other bucket too
(`pending_purges.migration_copy`, migration 115, kept). The move holds the
row, so a purge either waits for it and then owes the copy it made, or ran
first and leaves nothing to move.

### What is kept, changed and deleted

| Mechanism | Fate |
|---|---|
| `objects.upload_bucket_id` | kept; no longer cleared by the promote, cleared by the settle; set on every row with a URL or a copy |
| `presign_expires_at` | kept; read by the settle and the completion condition |
| promote fence (`checkPlacement`, `holdMigrationFence`, `FencingPromotes`, `ErrBindingMoved`) | deleted; the promote checks its bucket against the row's anchor |
| start and bindings locks, `ErrBucketMigratedAway` | deleted |
| `copy_started_at`, catch-up, `storage_migration_caught_up`, `ErrCopyBehind` | deleted |
| `rebound_at`, `rebound_back_at`, copy back, `storage_migration_copied_back`, `ErrRollbackBehind`, `rollback_cursor_*` | deleted |
| late-write moves (`PromoteLateSourceWriteInTx`, `LateWriteClockMargin`, the ingest's `Late`/`Mover`) | deleted; an event applies only to its row's anchor |
| `storage_migration_source_etags`, `ErrCleanupKept` | deleted |
| relocation at completion (`uploadedBytes`, `multiparth.settle`) | deleted; an upload is promoted where it is |
| states `rebinding`, `verifying` | deleted; `moving` added; `provisioning`, `completed`, `cleaned`, `rolling_back`, `failed` kept |
| `cursor_collection` / `cursor_path` | kept, for the cleanup's walk |
| `cleanup_retention_seconds` | kept, floored at `max_ttl` |
| `pending_purges.migration_copy` | kept |
| `storage_layout` | flips on entering `moving`, back on entering `rolling_back` |

## Consequences

- **A migration cannot finish before the last URL signed for its source
  expires** — at most `max_ttl` (seven days) plus the reconciler's grace after
  it starts moving — nor clean before `max_ttl` after it finishes. That is
  the price of never having to know when a write landed.
- An object uploaded shortly before a migration is read from the source until
  its URL expires; an `AVAILABLE` object costs one HEAD more in its life, the
  settle's, whether or not a migration ever runs. In exchange a lost storage
  event — for a first upload or a later write through the same URL — is
  found within the grace, in either completion mode.
- A move holds its object's row for the length of one copy, as the current
  copy does.
- A presigned GET binds `If-Match` to the row's ETag; after a cross-backend
  move the ETag is the target's, so a URL signed after the move binds the new
  one.
- Bytes a URL of a permanently deleted or discarded row writes stay in that
  bucket under no row. They were never promoted and are never read; nothing
  in this design deletes them (BACKLOG).
- The operator RPCs and messages keep their shape; the state names and the
  meaning of the two counters change (comments in `tenant_service.proto`).
  On the dev cluster a migration in flight across the deploy is abandoned:
  its data is disposable, and the new schema drops the columns it ran on.

## Options considered

- **Keep patching the instant, and give the operator an RPC for what is
  left.** The windows above are what remained after five rounds of this; the
  four open BACKLOG entries need an event-delivery bound that no transport
  gives. An RPC to settle a stuck key moves the judgement to a person, who has
  no better evidence than the worker.
- **Freeze writes to the source while it is copied** with a bucket policy
  denying `PutObject` to presigned requests. It removes late writes at the
  source, but SeaweedFS — the dev cluster's store — has no bucket policies
  (ADR-0026 probes them, and BACKLOG records the stores that lack them for
  public collections), and a freeze fails every upload of the tenant for the
  length of the copy. A per-store mechanism would have to be built for each
  store anyway.
- **Per-object placement** — this decision. It needs one column, a settle
  step the reconciler mostly already does, and a worker that is simpler than
  the one it replaces; the cost is the wait for URLs to expire.

## Stages

Each stage is its own pull request, merged before the next starts.

1. **Placement, behaviour unchanged.** Migration 121 adds
   `objects.placement_bucket_id`, backfilled from the binding for every row
   with confirmed bytes. Every promote, copy and the current migration's
   rebind and rebind back keep it equal to where the bytes are, and every
   path above reads bytes by it. While no migration runs it always equals the
   binding, so nothing observable changes; tests pin each read path to the
   placement where it differs from the binding.
2. **The migration moves objects.** The anchor kept and settled; the promote
   checked against the anchor; the event rule; regeneration and part URLs
   signed for the anchor under the deadline; completion without relocation;
   the worker rewritten as above, with rollback as the reverse move. The
   promote fence, the late-write paths and the catch-up stop being called.
   Deterministic integration tests: a late source write through an old URL is
   moved; an overwrite through an old and a new URL leaves the last promoted;
   the cleanup waits for the deadline and the anchors; a lost event is
   settled by the reconciler.
3. **The old machinery is deleted.** The tables, columns, code and tests
   listed above, the closed BACKLOG entries, and the proto comments; this ADR
   becomes Accepted.
