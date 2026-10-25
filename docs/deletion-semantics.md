# Deletion: what each delete actually destroys

Deleting in Paladin happens on two axes that are easy to conflate, and every
surprise in this document comes from conflating them:

- **Row vs bytes.** Removing an object's row and removing its bytes from the
  storage backend are separate steps with separate durability. `paladin.object.deleted`
  means the row changed state; `paladin.object.purged` means the bytes are gone.
- **Soft vs hard.** Tenants and objects go to a trash first and can be restored.
  Backends, buckets and collections have no trash — their delete is immediate.

Below, "blast radius" means: what disappears without a second confirmation, and
what refuses to disappear at all.

## The foreign keys decide more than the API does

Most of the behaviour operators notice is not in a handler. It is in the FK
actions on the schema, which fall into two groups: children that vanish with
their parent, and children that hold their parent hostage.

| Parent | `ON DELETE CASCADE` — vanishes silently | `ON DELETE RESTRICT` — blocks the delete |
| --- | --- | --- |
| `tenants` | `quotas`, `api_tokens`, `refresh_tokens`, `oauth_authorization_codes`, `operations`, `event_subscriptions`, `event_deliveries`, `object_locks`, `multipart_uploads`, `idempotency_keys` (every partition), `tenant_default_bindings`, `tenant_storage_migrations`, `tenant_slug_history`, `tenant_rate_buckets`, `erased_objects`, `bucket_grants` | `users`, `user_settings`, `buckets.owner_tenant_id`, `collections` |
| `storage_backends` | `storage_backend_health`, `storage_backend_features` | `buckets` |
| `buckets` | `replication_state`, `bucket_quotas`, `bucket_grants` | `collections`, `tenant_default_bindings`, `tenant_storage_migrations`, `multipart_uploads`, `pending_purges`, `pending_multipart_aborts` |
| `collections` | `erased_objects` | `objects` |
| `objects` | `object_versions`, `multipart_uploads` | — |

Two things are worth reading off that table directly.

**Data keeps its owner alive.** A tenant with collections cannot be hard-deleted,
and no flag changes that — the FK refuses. This is deliberate: the alternative
is an operator erasing a tenant's storage with one call.

**Capabilities go with their tenant, charges included.** The `limes` schema
keeps no foreign key to `tenants`, so no row of it appears in the table above:
the purge erases them through pgstore, in the same transaction as the tenant
row. Every capability of the tenant is revoked; its counters, holds, budget
and charges are deleted. A capability not yet expired for
`limes.MinPurgeRetention` stays on record, revoked, until the capability
purger removes it — an id no longer on record is not revoked, and its token
would verify again. Billing history does not outlive the tenant it belongs to.

## Tenant

| Call | Effect | Reversible |
| --- | --- | --- |
| `DeleteTenant` | Moves to trash (`deleted_at` set). Nothing is destroyed. | `RestoreTenant` |
| `PurgeTenant` | Hard delete of a **trashed** tenant. Refuses while the tenant owns anything (`RESTRICT`). | No |
| `PreviewPurgeTenant` | Changes nothing: names the API tokens and event subscriptions a purge would remove, counts them and the capabilities, and counts the `RESTRICT` children that would refuse it. | — |

**A tenant in the trash is frozen.** Every change to it is refused with
`FAILED_PRECONDITION` and reason `TENANT_ALREADY_DELETED`, whoever asks —
platform admins included — so that restoring returns exactly what was
trashed. What stays open: reading and downloading its data, taking access
away (revoking tokens and capabilities, cancelling operations, aborting
uploads), and the tenant's own lifecycle (`GetTenant`, `ListTenants`,
`RestoreTenant`, `PurgeTenant`). To remove a tenant that already holds data
in the trash, restore it, empty it, and trash and purge it.

The workers hold to the same rule: lifecycle expiry, replication, emptying the
object trash, settling pending uploads (from the reconciler or a storage
event), storage-layout migrations, queued batch operations and event delivery
all pass a trashed tenant by, and pick up where they were on restore. What
still runs is cleanup of work already done — the debt of committed deletes,
aborting abandoned multipart uploads — and recomputing quota usage.

**The trash has a retention, and it only surfaces.** `admin.tenants.trash_retention`
(default `720h`, 30 days; must be positive) is how long a tenant sits in the
trash before it is due for review. Every read of a trashed tenant carries
`purge_eligible_at` = `deleted_at` + that retention, computed from the current
configuration — so changing it moves the date for tenants already in the
trash. The console's Trash page shows each tenant's age ("In trash 12 days")
and marks it **Past retention** from that moment on. Nothing purges a tenant
when its retention lapses: purge is irreversible and `PurgeTenant` refuses
while the tenant still owns collections or locked objects, so it stays an
operator's call. The worker publishes the count of tenants past retention as
`paladin_tenants_past_trash_retention`, every `worker.jobs.housekeeping.interval`.

There is no one-shot hard delete. There used to be — `DeleteTenant(force=true)`
— and it is gone: landing the tenant in a different state is a different
transition, not a modifier on this one.

When a hard delete does go through, the CASCADE column above goes with it and
nothing asks again. Two entries deserve attention before you run it:

- `api_tokens` — every integration authenticating as that tenant stops working
  at once.
- `event_subscriptions` — subscribers stop receiving events. Nothing
  errors on their side; the deliveries just stop.

## Storage backend

`DeleteBackend` counts the buckets that reference the backend and refuses while
any exist. There is no override: `buckets.backend_id` is `ON DELETE RESTRICT`,
so the database refuses whatever the caller asks for. The count exists to say
how many, in a sentence an operator can act on, rather than as a
SQLSTATE.

Deleting a backend never touches the object storage it points at. It removes
Paladin's registration of it.

## Bucket

`DeleteBucket` removes the **registration**. The physical bucket on S3 is only
touched when `delete_on_backend=true`, which routes the row through a `deleting`
state for the worker to finish; without it the row is deleted immediately and
cleaning up the remote bucket is the operator's business.

`skip_version_check` on this call waives the OCC guard and nothing else. It
was called `force` until that word was found to mean four different things
across four entities; destructiveness lives in `delete_on_backend`, which
names what it destroys.

## Collection

`DeleteCollection` refuses while the collection still holds objects — trashed
ones included, since a trashed object keeps its row. No flag overrides that —
`objects.collection_id` is `ON DELETE RESTRICT`. The refusal is
`FAILED_PRECONDITION` with reason `COLLECTION_NOT_EMPTY`; its message says how
many live and how many trashed objects hold the collection, and its
`ErrorInfo` metadata carries the two counts as `live_objects` and
`trashed_objects`. Purge or restore the trashed ones, delete the live ones,
then delete the collection. The proto
used to claim `force=true` did; the handler never read it for that and could
not have. `skip_version_check` waives the OCC guard, which is all it ever did.

## Object

`DeleteObject` moves the object to the trash; `RestoreObject` brings it back.
`permanent=true` destroys it irrecoverably.

Nothing empties the object trash by default. With
`worker.jobs.housekeeping.hard_delete_after` above zero (default `0s`, off),
the worker's `LifecycleHardDeleter` removes objects trashed longer than that:
the row first, recorded as a purge debt, then the bytes, with
`paladin.object.purged` once they are gone. Bucket lifecycle rules only move
expired objects to the trash; they never delete permanently.

`resource_version` is **required on every delete**, including the permanent
one. This is not ceremony: `expected_version=0` disables the check in SQL, so
an absent guard silently becomes a blind delete.

Object lock outranks all of it:

- **Legal hold** and **COMPLIANCE** retention are absolute. No role, no flag.
- **GOVERNANCE** retention yields only to `bypass_governance_retention=true`
  from a caller holding `lock.governance.bypass` or `platform.admin`. The
  server sets `paladin.bypass_governance_retention` for that transaction and a
  trigger on `object_locks` reads it before allowing the row to go.

### Deleted from the bucket, not through Paladin

When the ingest plane receives a delete event from the storage backend, it
does not take the event's word for it. The physical key carries no object
id, so the same key can belong to a newer object or to one whose bytes have
moved to another bucket. The ingest plane HEADs the object where its row
places it now, and only an AVAILABLE object whose bytes are not there moves
to the trash, with a `paladin.object.deleted` event (`mode: soft`, `source:
storage_event`).

This applies to public objects too: the trash is refused for them only
because a soft delete leaves their bytes served, and these bytes are already
gone. No delete marker is written in a versioned bucket, because all of an
object's versions share its physical key and the backend's delete removed
them all. `RestoreObject` refuses to restore an object whose bytes are
missing.

### In a public collection

A public collection ([ADR-0027](adr/0027-public-collections.md)) has no trash.
Its objects are served to anyone at their URL, and a soft delete leaves the
bytes in storage, so the URL would keep answering: `DeleteObject` without
`permanent=true` is refused (`FAILED_PRECONDITION`, reason
`PUBLIC_COLLECTION_RULE`), and a batch delete reports each such object as
failed. A permanent delete removes the bytes, and the store answers 404 at the
URL from then on. A CDN in front of the bucket may keep serving its copy until
its own TTL expires; that is between the CDN and whoever configured it.

Moving the tenant to the trash does not touch any bytes, so its public objects
stay readable until they are deleted.

## Multipart sessions

An in-flight multipart upload holds parts on the storage backend that only
`AbortMultipart` releases, and only two callers issue it: the Abort RPC and
`MultipartReaper`. Both work from the `multipart_uploads` row — so a row
removed any other way used to leave the S3 session open forever, accruing
part-storage charges with nothing left in the system that knew about them.

`ON DELETE CASCADE` from `objects` did exactly that: permanently deleting a
PENDING object dropped the session. A trigger now turns any such deletion into
a `pending_multipart_aborts` row, and `MultipartAbortDrainer` discharges it —
the same debt-and-drainer shape the object bytes use below. The Abort RPC and
the reaper set `paladin.multipart_aborted` for their transaction, which tells
the trigger they have already done the work.

## Bytes, and the debt when they survive

Row first, bytes second — deliberately. The reverse order can delete a live
object's bytes and then fail to remove its row, which is unrecoverable; this
order leaves at worst bytes with no row.

That residue is not left to chance. The handler writes a `pending_purges` row
in the same transaction that deletes the object, then attempts the byte-delete
itself. Whatever that attempt cannot finish, `PurgeDrainer` retries — the retry
half of a transactional outbox over bytes ([ADR-0003](adr/0003-transactional-outbox.md)).
S3 `DELETE` on an absent key succeeds, so re-issuing a delete that already
landed costs nothing, which is what makes "leave the debt row on any doubt" the
correct failure policy.

Until that worker existed, the residue was unrecoverable: nothing in the schema
remembered where those bytes were, and the only way to find one was to list the
bucket.

## Practical order for removing a tenant completely

1. Delete or purge its objects (`permanent=true`, or let
   `hard_delete_after` expire them from the trash).
2. Delete its collections (possible once no object is left, the trash included).
3. Delete its buckets — with `delete_on_backend=true` if the remote bucket
   should go too.
4. Delete its users.
5. `DeleteTenant` (to the trash), then `PurgeTenant`.

Skipping ahead does not corrupt anything; the FK refuses and the step fails.
