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
| `tenants` | `quotas`, `tenant_budgets`, `api_tokens`, `refresh_tokens`, `oauth_authorization_codes`, `operations`, `event_subscriptions`, `event_deliveries`, `object_locks`, `object_tags`, `multipart_uploads`, `capability_records`, `capability_reservations`, `idempotency_keys` (every partition), `tenant_default_bindings`, `tenant_storage_migrations`, `tenant_slug_history`, `tenant_rate_buckets` | `users`, `user_settings`, `buckets.owner_tenant_id`, `collections`, `charges`, `charge_refunds` |
| `storage_backends` | `storage_backend_health` | `buckets` |
| `buckets` | `replication_state`, `bucket_quotas` | `collections`, `tenant_default_bindings`, `tenant_storage_migrations`, `multipart_uploads`, `pending_purges`, `pending_multipart_aborts` |
| `collections` | — | `objects` |
| `objects` | `object_versions`, `multipart_uploads` | — |

Two things are worth reading off that table directly.

**Data keeps its owner alive.** A tenant with collections cannot be hard-deleted,
and no flag changes that — the FK refuses. This is deliberate: the alternative
is an operator erasing a tenant's storage with one call.

**`charges` restricts on purpose.** The billing trail is not a child that
follows its tenant into the void; a tenant with charges must have them dealt
with explicitly.

## Tenant

| Call | Effect | Reversible |
| --- | --- | --- |
| `DeleteTenant` | Moves to trash (`deleted_at` set). Nothing is destroyed. | `RestoreTenant` |
| `PurgeTenant` | Hard delete of a **trashed** tenant. Refuses while the tenant owns anything (`RESTRICT`). | No |

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

`DeleteCollection` refuses while the collection still holds objects. No flag
overrides that — `objects.collection_id` is `ON DELETE RESTRICT`. The proto
used to claim `force=true` did; the handler never read it for that and could
not have. `skip_version_check` waives the OCC guard, which is all it ever did.

## Object

`DeleteObject` moves the object to the trash; `RestoreObject` brings it back.
`permanent=true` destroys it irrecoverably.

`resource_version` is **required on every delete**, including the permanent
one. This is not ceremony: `expected_version=0` disables the check in SQL, so
an absent guard silently becomes a blind delete.

Object lock outranks all of it:

- **Legal hold** and **COMPLIANCE** retention are absolute. No role, no flag.
- **GOVERNANCE** retention yields only to `bypass_governance_retention=true`
  from a caller holding `lock.governance.bypass` or `platform.admin`. The
  server sets `paladin.bypass_governance_retention` for that transaction and a
  trigger on `object_locks` reads it before allowing the row to go.

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

1. Delete or purge its objects (`permanent=true`, or let the trash TTL expire).
2. Delete its collections (possible once the objects are gone).
3. Delete its buckets — with `delete_on_backend=true` if the remote bucket
   should go too.
4. Delete its users.
5. `DeleteTenant` (to the trash), then `PurgeTenant`.

Skipping ahead does not corrupt anything; the FK refuses and the step fails.
