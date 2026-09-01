# Upgrading

## Changing the API contract

`buf breaking` runs in CI against the `api/v0.5.0` tag and **blocks**. Pre-1.0
the project still breaks compatibility deliberately — see *Project status* in
the README — so the gate does not forbid it. It forbids doing it by accident.

To land a deliberate breaking change:

1. Make the change and let CI fail on it. Read the failure: `buf` names the
   message, the field number and what changed.
2. Decide it is worth it. The common case is a field rename that keeps the
   number — gRPC clients survive that, JSON clients do not, and nothing at
   runtime will tell you which you broke.
3. Note it here, under a heading for the release.
4. Cut the next baseline once the change is merged:

   ```
   git tag -a api/v0.5.1 -m "…what changed and why"   # the NEXT number
   git tag -f api/latest
   git push origin api/v0.5.1 && git push --force origin api/latest
   ```

   Then bump `breaking_against` in `.github/workflows/test.yml` to it.

   A new number rather than `-f` on the current one: the tags are the record
   of what the contract WAS at each point, and force-moving a published tag
   redefines it under anyone who pinned it. `api/latest` is the only one that
   moves, which is what its name promises. The tag history — v0.1.0 through
   v0.5.0 — is what this procedure has actually been doing; the instruction to
   force-move said otherwise and was wrong.

The baseline is a tag rather than the default branch on purpose: `main` and
`develop` advance together in this repo, so comparing against `main` compares
the tree with itself and passes without checking anything.


## Unreleased — 36 read RPCs declare `idempotency_level = NO_SIDE_EFFECTS`

A deliberate breaking change under the procedure above: `buf` puts
`RPC_SAME_IDEMPOTENCY_LEVEL` in the WIRE category, so declaring the option at
all trips the gate. Nothing about the request or response bytes changes.

**What it means for a client.** `NO_SIDE_EFFECTS` tells gRPC intermediaries the
call is safe to retry on their own. For these 36 it is: each was checked
against its handler, not against its name. If you run a proxy that acts on the
option, expect it to start retrying these reads — which is the point.

**Why only 36 of 142.** The remainder are unannotated, and
`IDEMPOTENCY_UNKNOWN` — the default — is the honest value for them: it promises
nothing. Annotating the rest needs the same per-handler check, and a wrong
annotation is a wrong wire contract that costs another baseline to fix. Three
draft classifications built from method-name prefixes each got RPCs wrong
(`RotateCredentials` is not idempotent; `BatchDeleteObjects` is), and the proto
comment on `TestBackend` says "Read-only" while the handler persists the probe
result via `SetHealth`. Names and comments were not a usable source; handlers
were.

**What keeps it true.** `internal/api/idempotency_contract_test.go` parses
every handler behind a NO_SIDE_EFFECTS RPC on each build and fails if it
reaches a write, directly or one hop through a helper. Verified by mutation in
both shapes.

**Cutting the baseline.** This needs `api/v0.6.0`; see the procedure above.
Until it is tagged and `breaking_against` bumped, `task verify-all` fails on
this change by design.

## Unreleased — API tokens are addressed by resource name

`APITokenService` was the only service in the API where `name` did not mean a
resource name, and the only one whose RPCs took a bare uuid.

| Message | Was | Now |
| --- | --- | --- |
| `APIToken.name` | the operator's label, e.g. `ci-uploader` | the resource name, `tenants/{tenant}/apiTokens/{id}` |
| `APIToken.display_name` | — | the label (field 3, formerly `name`) |
| `Create.tenant_id` | bare uuid | `parent`, `tenants/{tenant}` |
| `Create.name` | the label | `display_name` |
| `List.tenant_id` | bare uuid | `parent`, `tenants/{tenant}` |
| `Revoke.id` | bare uuid | `name`, `tenants/{tenant}/apiTokens/{id}` |
| `GetUsage.id` | bare uuid | `name`, same shape |
| `GetUsageResponse.id` | bare uuid | `name`, echoes the request |

**Why it had to change rather than being tidied later.** `api_tokens` carries
FORCE row-level security keyed on the session tenant. An RPC holding only an id
cannot scope its query, and cannot discover the tenant either — the row that
would tell it is the one RLS is hiding. So `Revoke` and `GetUsage` were
unreachable across tenants by construction: a platform admin could see another
tenant's tokens and not revoke one, which is the operation the visibility is
for. Carrying the parent in the address breaks that circle without adding a
privileged read path around the enforcement mechanism.

**What callers have to change**

- `Create({tenant_id, name})` → `Create({parent: "tenants/<t>", display_name})`.
- `List({tenant_id})` → `List({parent: "tenants/<t>"})`.
- `Revoke({id})` → `Revoke({name: "tenants/<t>/apiTokens/<id>"})`.
- `GetUsage({id})` → same `name` shape; the response field is `name` too.
- Anything reading `APIToken.name` as a label reads `display_name` now. Note
  this is the dangerous one for a JSON client: the field still exists and still
  carries a string, so nothing fails — it just renders a resource name where a
  label used to be.

`APIToken.id` and `tenant_id` are unchanged and still returned, so a caller
that wants the raw parts does not have to parse the name.

## Unreleased — a failed operation fills `Operation.error`, not `Operation.response`

`Operation.result` is a oneof of `google.rpc.Status error` and
`google.protobuf.Any response`. Both API planes used to put every stored
payload — failures included — into `response`, and never set `error` at all.
The stored `error_code` and `error_message` reached no client, and a caller
that asked `result.case == "error"` to tell a failure from a success got
"success" for every failed operation. The console's ops drawer and dashboard
widget both asked exactly that.

Now an operation in FAILED or CANCELLED fills `error`:

- `code` — the canonical code: `CANCELLED` for a cancellation, `ABORTED` for
  `WORKER_LOST` (the work may be half-applied), `UNIMPLEMENTED` for an
  unknown operation type, `UNKNOWN` otherwise.
- `message` — the stored `error_message`, falling back to the code when the
  row has no message.
- `details[0]` — the payload that used to be in `response`, unchanged, as an
  `Any`-wrapped `Struct`. A reclaimed operation's payload also carries
  `last_progress: {processed, total}`: the last snapshot the worker wrote
  before it died, which is a lower bound — progress writes are throttled to
  about one a second.

A client that read failure detail out of `response` needs to read
`error.details[0]` instead. A client that only reads successes is unaffected:
the `response` arm still carries them.

## Unreleased — `force` is gone from the delete RPCs

One word meant four different things, and two of them were dangerous.

| RPC | `force` used to mean | Now |
| --- | --- | --- |
| `DeleteTenant` | skip the trash and hard-delete | **removed** — `DeleteTenant` trashes, `PurgeTenant` destroys |
| `DeleteBackend` | "delete even if buckets reference it" | **removed** — it never could; `buckets.backend_id` is `ON DELETE RESTRICT` |
| `DeleteBucket` | waive the OCC guard | renamed `skip_version_check` |
| `DeleteCollection` | documented as "ignore objects", implemented as "waive OCC" | renamed `skip_version_check`, doc corrected |

An integrator who learned the word on tenants and applied it to buckets was
turning off the concurrency check while believing they were insisting harder.
That is the kind of mistake an API should make impossible to make.

`skip_version_check` now means exactly one thing everywhere it appears: waive
the optimistic-concurrency guard. Nothing else in the API is spelled `force`.

**What callers have to change**

- `DeleteTenant(force=true)` → `DeleteTenant(...)` then `PurgeTenant(...)`.
  Two calls, and the destructive one names itself.
- `DeleteBackend(force=true)` → delete the buckets first. The flag never
  worked; it only turned a clear conflict into a raw SQLSTATE under
  `Internal`.
- `DeleteBucket(force=…)` / `DeleteCollection(force=…)` → rename the field to
  `skip_version_check`. Same meaning, same behaviour.
- `resource_version` is now required by the schema on `DeleteTenant` and
  `DeleteBackend`, not by a handler branch — with no bypass flag left, there
  is nothing for an omission to be legitimate for.

Field numbers 3 on `DeleteTenantRequest` and `DeleteBackendRequest` are
reserved, so nothing can silently reuse them.


## v4.0.0 — the `paladin` rename

The project was renamed from `paladin` / `paladin` to `paladin`
on 2026-08-19. The rename went all the way down: it changed runtime
contracts, not only source identifiers.

**The supported upgrade path is to reprovision from scratch.** There is
no in-place migration, and none is planned.

This is deliberate. Pre-1.0 the project does not carry a
backward-compatibility promise (constitution, Principle IV), and the
rename touches Postgres roles, row-level-security policy internals,
credential formats, wire paths and the environment-variable prefix at
once. A migration that got any one of those half-right would leave a
cluster in a state harder to diagnose than a clean install — see
*Two silent failures* below for why.

If you are installing Paladin for the first time, none of this applies.
Follow [the README](../README.md); a fresh install is already correct.

### What reprovisioning means

You lose the control-plane database: tenants, buckets, Collections,
capabilities, API tokens, event subscriptions, and the audit log. You do
not lose object bytes — those live in the S3 backend and are untouched —
but with the `objects` rows gone they are orphaned, referenced by
nothing. Treat them as garbage to be collected, or re-import them
deliberately; Paladin will not adopt them on its own.

If that is not acceptable for your deployment, do not upgrade. Dump what
you need first, or stay on v3.

### Procedure

1. Take a dump of the old database if you want any of it for reference.
   Nothing in v4 reads it.
2. Reissue every API token. Old tokens carry the `paladin_pat_` prefix and
   are not recognised — see below. There is no dual-prefix grace period.
3. Rewrite every `PALADIN_*` environment variable to `PALADIN_*`. Do this
   before starting the new pods, not after.
4. Drop the old database and roles, or point the new deployment at a
   fresh one.
5. Deploy v4 and let `migrate` build the schema from empty.
6. Run `bootstrap` to create the platform admin.
7. Regenerate every client from the new protos.

### Two silent failures to watch for

Most of the rename fails loudly. Two do not, and both look like a
working system:

- **Stale RLS GUC.** Policies read a session-local GUC that moved from
  `paladin.tenant_id` to `paladin.tenant_id`. A v4 pod against v3 policies
  sets a GUC nothing reads, so every tenant-scoped query matches zero
  rows. No error, no log line — an empty console that looks like an
  empty deployment. This is the failure mode that makes a
  partially-applied migration worse than a clean install.
- **Ignored environment variables.** The config loader keys on the
  `PALADIN_` prefix. Anything still named `PALADIN_*` is not rejected, it is
  simply not seen, and the process starts on defaults — 57 variables
  can go quiet at once. Grep your manifests before deploying, not
  after.

### The complete list of breaking changes

| Contract | v3 | v4 |
| --- | --- | --- |
| Postgres roles | `paladin_app`, `paladin_migrate`, `paladin_reaper` | `paladin_app`, `paladin_migrate`, `paladin_reaper` |
| RLS session GUC | `paladin.tenant_id` | `paladin.tenant_id` |
| RLS helper function | `paladin_session_tenant_id()` | `paladin_session_tenant_id()` |
| API token prefix | `paladin_pat_…` | `paladin_pat_…` |
| Event type names | `paladin.object.uploaded`, `paladin.bucket.updated`, … | `paladin.*` |
| Proto package / RPC paths | `/paladin.data.v1.ObjectService/GetObject` | `/paladin.data.v1.ObjectService/GetObject` |
| HTTP headers | `X-PALADIN-*` | `X-Paladin-*` |
| Environment prefix | `PALADIN_` (57 variables) | `PALADIN_` |
| OAuth scopes | `paladin.read`, … | `paladin.*` |
| Default bucket names | `paladin-primary`, `paladin-archive`, `paladin-data` | `paladin-primary`, `paladin-archive`, `paladin-data` |
| Go module path | `github.com/oleg-tkachuk/paladin` | `github.com/oleg-tkachuk/paladin` |
| Chart / image names | `paladin`, `paladin-*` | `paladin-core`, `paladin-console` |

Two entries need a word of explanation.

**Default bucket names** are config defaults, not a rename applied to
your storage. Existing buckets keep the names they have. Either override
them in config or create the new ones — Paladin does not rename a bucket
under you.

**Event type names** break subscribers that filter on type. A consumer
matching `paladin.object.uploaded` goes quiet rather than erroring, in the
same way the environment variables do. Update filters as part of the
cutover, not afterwards.

### The schema is one baseline, not a history

The 65 incremental migrations were replaced by three ordered files —
`001_initial_schema.sql`, `002_roles_and_rls.sql`, `003_triggers.sql` —
split by kind rather than by date. `001`'s down migration drops the
schema outright.

This reinforces the reprovision-only path rather than adding to it:
Goose has no record of the old versions, so pointing the new baseline at
a v3 database is not a supported operation and will not be made one.
Fresh installs are unaffected — the baseline builds the same schema the
65 files built, minus the dead ends they accumulated on the way.

### Resource rename: ObjectKey became Collection

The logical namespace an object lives in was called an *ObjectKey*, which
collided with "object key" in the S3 sense — the path within a bucket.
It is now a **Collection**, and the rename runs through the schema, the
protos, the console URLs and the Cedar action names.

| Contract | before | after |
| --- | --- | --- |
| Table | `object_keys` | `collections` |
| Proto service | `ObjectKeyService` | `CollectionService` |
| Cedar actions | `ManageObjectKey`, `BindObjectKeyToBucket` | `ManageCollection`, `BindCollectionToBucket` |
| Resource name | `tenants/{t}/objectKeys/{ok}` | `tenants/{t}/collections/{c}` |
| Console route | `/tenants/{id}/object-keys/…` | `/tenants/{id}/collections/…` |
| Storage path segment | `<tenant>/<object_key>/<key>` | `<tenant>/<collection>/<key>` |

**A Cedar policy that names the old actions does not error — it stops
matching.** Update policies as part of the cutover. The same applies to
anything parsing the storage path layout.

### Bucket references travel as one field

A reference to another resource is its name, not its parts (AIP-122).
Requests and messages that carried a `(backend_id, bucket_name)` pair now
carry a single `bucket` holding
`storageBackends/{backend_id}/buckets/{bucket_id}` — `TenantDefaultBinding`,
`SetTenantDefaultBinding`, `StorageMigrationStatus`, and `Collection.bucket`
among them. Clients that set the halves separately will fail validation
rather than silently binding to the wrong place.

### Attribution is mandatory where it was optional

Rows that record *who did something* now require it: `multipart_uploads`
carries the initiating principal, `capability_records.created_by` and
`api_tokens.created_by` are `NOT NULL`, and `capability.Store.Insert`
takes an `issuedBy Principal` argument. Previously these were nullable or
defaulted to the empty string, which made "who owns this upload" a
question the data could not answer.

Custom `capability.Store` implementations must be updated for the widened
signature — it is a compile-time break, not a runtime one.

### Deployments in a cluster

The Helm release name changed, so the old `paladin*` /
`paladin-*` Deployments, Services, ServiceAccounts, Certificates and Linkerd
Servers are pruned and `paladin-core-*` / `paladin-console` ones created in
their place. mTLS certificates regenerate (SANs `paladin-core-api` /
`paladin-core-admin`, SPIFFE `…/sa/paladin-core-api`). Expect a brief
in-namespace disruption, and confirm afterwards that the `/login`
redirect, the BFF's backend health aggregation and internal mTLS have all
recovered.
