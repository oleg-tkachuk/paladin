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
| `tenants` | `quotas`, `tenant_budgets`, `api_tokens`, `refresh_tokens`, `oauth_authorization_codes`, `operations`, `event_subscriptions`, `event_deliveries`, `object_locks`, `object_tags`, `multipart_uploads`, `capability_records`, `idempotency_keys` (every partition), `tenant_default_bindings`, `tenant_storage_migrations`, `tenant_slug_history`, `tenant_rate_buckets` | `users`, `user_settings`, `buckets.owner_tenant_id`, `collections`, `charges` |
| `storage_backends` | `storage_backend_health` | `buckets` |
| `buckets` | `replication_state`, `quotas` | `collections`, `tenant_default_bindings`, `tenant_storage_migrations`, `multipart_uploads`, `pending_purges` |
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
| `DeleteTenant(force=true)` | Hard delete, skipping the trash. Refuses while the tenant owns anything (`RESTRICT`). | No |
| `PurgeTenant` | Hard delete of a **trashed** tenant. Same `RESTRICT` refusal. | No |

When a hard delete does go through, the CASCADE column above goes with it and
nothing asks again. Two entries deserve attention before you run it:

- `api_tokens` — every integration authenticating as that tenant stops working
  at once.
- `event_subscriptions` — subscribers simply stop receiving events. Nothing
  errors on their side; the deliveries just stop.

## Storage backend

`DeleteBackend` counts the buckets that reference the backend and refuses while
any exist. `force=true` skips **that count**, not the foreign key — `buckets`
is `ON DELETE RESTRICT`, so the delete still fails, just further down. Both
refusals surface as a conflict with the same actionable message; the flag
changes which layer says no, not the answer.

Deleting a backend never touches the object storage it points at. It removes
Paladin's registration of it.

## Bucket

`DeleteBucket` removes the **registration**. The physical bucket on S3 is only
touched when `delete_on_backend=true`, which routes the row through a `deleting`
state for the worker to finish; without it the row is deleted immediately and
cleaning up the remote bucket is the operator's business.

`force` on this call means "skip the OCC guard" and nothing else. The two used
to share one field, which put the most destructive form of the call — the one
that also erases the physical bucket — on the only path that skipped the
concurrency check.

## Collection

`DeleteCollection` refuses while the collection holds objects that are not
already DELETED, unless `force=true`. `force` also waives the OCC guard.

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
2. Delete its collections (`force=true` once the objects are gone).
3. Delete its buckets — with `delete_on_backend=true` if the remote bucket
   should go too.
4. Delete its users.
5. `DeleteTenant`, then `PurgeTenant`.

Skipping ahead does not corrupt anything; the FK refuses and the step fails.
