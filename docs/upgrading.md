# Upgrading

## Changing the API contract

`buf breaking` runs in `verify-all`, and so in CI, against the tag
pinned as `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh`, and
**blocks**. Pre-1.0 the project still breaks compatibility deliberately — see
*Project status* in the README — so the gate does not forbid it. It forbids
doing it by accident.

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

   Then bump `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh` to it.

   A new number rather than `-f` on the current one: the tags are the record
   of what the contract WAS at each point, and force-moving a published tag
   redefines it under anyone who pinned it. `api/latest` is the only one that
   moves, which is what its name promises. The tag history — v0.1.0 through
   v0.5.0 — is what this procedure has actually been doing; the instruction to
   force-move said otherwise and was wrong.

The baseline is a tag rather than the default branch on purpose: a branch
moves with every merge, so comparing against `main` from `main` compares the
tree with itself and passes without checking anything.


## Unreleased — clients read the contract; the three prefix lists are gone

No proto change and no wire change. `backend/internal/rpcmeta` answers "is this
a read" and "should a client stamp an Idempotency-Key" from the descriptor, and
the console, the seeder, the MCP bridge and the server interceptor all ask it.

**What a client should send, stated once.** Stamp a key iff the method declares
`IDEMPOTENCY_UNKNOWN`. `NO_SIDE_EFFECTS` changes nothing, so a key buys a row
in `idempotency_keys` and a chance of serving a stale snapshot. `IDEMPOTENT` is
already safe to repeat, so a key buys a row and no safety. What is left is
every call whose repeat nobody has promised anything about.

**Two behaviours change**, and both are the descriptor being right where the
prefixes were not:

- `RestoreObjectVersion` no longer gets a key. The `Restore` prefix swept it
  in; it takes `resource_version`, so a repeat writes the same value or fails
  `Aborted`.
- Credential minting — `Login`, `RefreshToken`, `ExchangeAudience`,
  `SwitchTenant` — now does. Clients used to withhold it because replaying a
  rotated refresh token is wrong. That is true, and it is enforced on the
  server, which refuses to memoize those four at all
  (`middleware.CredentialMintingProcedures`). An Idempotency-Key is a caller's
  de-duplication token, not a request to cache.

If you integrate with Paladin, this is the rule to implement. `buf` puts
`idempotency_level` in every generated descriptor;
`@bufbuild/protobuf` exposes it as `method.idempotency`, and the Go runtime as
`MethodOptions.GetIdempotencyLevel()`.

## Unreleased — 112 of 142 RPCs declare an idempotency_level

Nine more: one read and eight idempotent writes. The remaining 30 are the
honest residue — every one of them creates, mints, charges or submits, and
`IDEMPOTENCY_UNKNOWN` is the right answer for all of them.

**`GetDispatcherStats` was never a write.** It was reported as one because the
verb list matched prefixes, and `Dispatch` swallowed `DispatcherStats` — an
outbound HTTP GET for the dispatcher's stats page. The verb now has to end at a
word boundary. A genuine read stayed undeclared for months for looking guilty.

**Two are OCC-guarded**: `RestoreObjectVersion` and `RenameTenantSlug` carry
`resource_version`, so a repeat writes the same value or fails `Aborted`.

**Six carry a written reason**, each read out of the SQL rather than inferred
from the verb — which is what made `RotateCredentials` look safe:
`SetObjectLegalHold` and `SetObjectRetention` are `ON CONFLICT DO UPDATE` to
the value supplied; `GrantScopes` merges through a dedup keyed on the scope
string, so it is a set union; `ResetPassword` sets the password to the one
given (the bcrypt hash differs per call because the salt does, but which
password works does not); `UpdateMine` upserts one settings row; and
`TestBackend` re-probes and overwrites the health row — the same RPC whose
proto comment says "Read-only" while it writes, which is why it is idempotent
and not a read.

**`RestoreTenant` and `ChangePassword` are deliberately left undeclared.** Both
converge on state, and both answer a repeat with an error: the restore query is
`WHERE deleted_at IS NOT NULL`, so a second call touches no row and returns
`ErrNotTrashed`, and `ChangePassword` checks the old password, which the first
call already invalidated. A proxy retrying either would report a failure for an
operation that succeeded. The OCC pair above answer `Aborted`, which is the
standard "re-read and retry" signal; these two do not.

**Cutting the baseline.** This needs `api/v0.9.0`.

## Unreleased — 103 of 142 RPCs declare an idempotency_level

Thirteen more `NO_SIDE_EFFECTS`, and they are the ones the earlier passes said
were unreachable: `GetHealth`, `GetQuota`, `GetObjectTags`, `GetObjectLock`,
`GetObjectVersion`, `ListObjectVersions`, `GetSubscription`,
`ListSubscriptions`, `GetTenantDefaultBinding`, `GetConfig`,
`GetPlatformStats`, `Summarize`, and `HealthService.GetVersion`.

**Why they were invisible, in three layers.** The guard globbed `handler*.go`,
a prefix match that never opened `lock_handler.go` or `version_handler.go`. It
accepted only `*Handler` receivers, while the Connect layer's are `*…Server`.
And it indexed per package, encoding an assumption the tree does not honour:
`QuotaService.GetQuota` is a method on `*QuotaServer` that dispatches into
`H.GetTenantQuota` — a different name, in a different package. Each of the
three hid the others. Handler bodies actually checked went from 52 to 108.

**The write detector was answering wrongly too.** A probe over what was still
undeclared reported `RenameTenantSlug`, `Delegate`, `Issue` and
`MigrateTenantStorageLayout` as writing nothing. They call `Rename`,
`Delegate`, `Mint` and `Migrate` — words the verb list did not have. Three
rounds of adding words is the honest measure of a name-based detector: it finds
what someone thought to name, which is why every annotation is still read by
hand and this only keeps it true afterwards.

**`DownloadObject` is deliberately NOT annotated.** With the deeper walk it
reports `RecordPresign` and `ChargeRequest`: it records the presign it issues
and charges the tenant's quota. It reads like a read and is not one.

**39 remain undeclared, and none of them look like a read** — the census test
prints the list on every run, so the remaining gap is visible rather than
inferred.

**Cutting the baseline.** This needs `api/v0.8.0`.

## Unreleased — 90 of 142 RPCs declare an idempotency_level

Extends the previous entry; the same procedure and the same reasoning. 54 more
declarations: 44 `IDEMPOTENT` and 10 `NO_SIDE_EFFECTS` on method names that
exist on two services each.

**What an intermediary may now do.** `IDEMPOTENT` invites a retry on failure.
Every RPC carrying it is one of three shapes, and a test enforces that: it
takes `resource_version` (a repeat either writes the same value or loses the
OCC check and fails Aborted), it removes something (removing what is gone is a
no-op), or it appears in `idempotentByArgument` with a written reason. An
earlier draft declared `RotateCredentials` idempotent — two rotations mint two
credential pairs — and that guard now fails on exactly that.

**Still unannotated: 52.** Most genuinely need an Idempotency-Key and
`IDEMPOTENCY_UNKNOWN` is the honest value. Fourteen do not: they are reads
whose handlers hang off receivers the guard's index did not reach
(`*VersionHandler` and similar). They stay unannotated until each is found and
checked, because an annotation nothing verifies is the failure this work is
about. `RestoreObjectVersion` is also held back deliberately — it makes a named
version current and demotes the previous one, and what a repeat does was not
clear enough from the code to promise anything.

**Cutting the baseline.** This needs `api/v0.7.0`.

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
Until it is tagged and `breaking_against` bumped, `verify-all` fails on
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
