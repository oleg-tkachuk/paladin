# Upgrading

## Changing the API contract

`buf breaking` runs in `verify-all`, and so in CI, against the tag
pinned as `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh`, and
**blocks**. The contract is pre-1.0 (`api/v0.x`) and still breaks
compatibility deliberately, so the gate does not forbid it. It forbids doing
it by accident.

To land a deliberate breaking change:

1. Make the change and let CI fail on it. Read the failure: `buf` names the
   message, the field number and what changed.
2. Decide it is worth it. The common case is a field rename that keeps the
   number — gRPC clients survive that, JSON clients do not, and nothing at
   runtime will tell you which you broke.
3. Note it in a file of its own, `docs/upgrading/<slug>.md`, headed
   `## Unreleased — <title>`, in the commit that makes the change; do not
   edit this guide. The release renames the heading to the tags that shipped
   it, reading which streams it belongs to from the commit that added the
   file ([`.github/ci/upgrade-sections.ts`](../.github/ci/upgrade-sections.ts)),
   and writes every file into this guide below, newest first: a section
   written in a commit of documentation alone reads as the product's and
   stays Unreleased through every SDK release. Until a release has run, a
   change's note is in `docs/upgrading/` alone. A note written straight into
   this guide conflicted with every release that named a section beside it.
4. Cut the next baseline once the change is merged:

   ```
   git tag -a api/v0.15.0 -m "…what changed and why"   # the NEXT number
   git tag -f api/latest
   git push origin api/v0.15.0 && git push --force origin api/latest
   ```

   Then bump `API_BASELINE_TAG` in `backend/scripts/proto-breaking.sh` to it.

   The baseline releases nothing. The SDKs release themselves, as
   `sdk/go/vX.Y.Z`, from the commits that touch `sdk/` or `proto/` — see
   [releasing.md](releasing.md).

   A new number rather than `-f` on the current one: the tags are the record
   of what the contract WAS at each point, and force-moving a published tag
   redefines it under anyone who pinned it. `api/latest` is the only one that
   moves, which is what its name promises. The tag history — v0.1.0 through
   v0.14.0 — is what this procedure has actually been doing; the instruction to
   force-move said otherwise and was wrong.

The baseline is a tag rather than the default branch on purpose: a branch
moves with every merge, so comparing against `main` from `main` compares the
tree with itself and passes without checking anything.

### A field keeps its meaning

`buf breaking` sees a field renamed or renumbered, never one whose meaning
changed under the same name. That is the change a client cannot catch: it
compiles, it runs, and it reads the wrong thing. So:

- A field that can be empty for more than one reason ships with a status
  beside it from its first release, naming why it is empty. The Biscuit on an
  Issue answer went empty for a capability without `share` with nothing
  saying so, and `biscuit_status` came two releases later.
- A field never changes what it means. Add a field, or a status value, and
  deprecate the old one; its section here says what to read instead.
- A value a client spells — an operation, a plane — is a type in both SDKs
  (`paladin.Op`, `paladin.Plane`), so a misspelling fails where it is written.

### Server and SDK

The Go and the Python SDK version apart (`sdk/go/vX.Y.Z`, `sdk/python/vX.Y.Z`)
and the server on its own (`vX.Y.Z`): a number in one says nothing of the
others. A section is headed by the tags of every release that carries its
change, and one whose change the server makes says, in a **Server:** line,
which server release a client sees it from.

Most server releases work with any SDK of the same contract. These need at
least the SDK named, and their sections say why:

| Server | Needs at least | Why |
| --- | --- | --- |
| `v39.0.0` | `sdk/go/v0.93.0` | the data plane's operations are namespaced; a caveat naming bare `presign` or `tag` is refused |
| `v27.5.0`, with `capability.dpop_nonce` on | `sdk/go/v0.62.0` | an older SDK cannot answer a DPoP nonce challenge |

A section that needs a newer SDK says so in its first lines, and adds its row
here.




<!-- sections: written by the release from docs/upgrading/, newest first; add a file there instead -->

## v41.1.0 — limes v1.0.0-rc.18: PostgreSQL 15 and pgstore's DPoP replay cache

- **PostgreSQL 15 or later.** pgstore v0.45 reads capability records through
  a `security_invoker` view, which an earlier PostgreSQL cannot create; the
  migration fails there.
- **DPoP proof ids move to `limes.dpop_seen_proofs`.** pgstore keeps the
  replay cache now, and migration `104` drops Paladin's `dpop_seen_proofs`.
  The new table starts empty: a proof accepted in the minute before the
  upgrade could be presented once more in that minute. Nothing to do.

## sdk/go/v0.128.0, sdk/python/v0.129.0 — the capability list takes an OAuth session

- `CapabilityService.List` takes `session`: the capabilities exchanged under
  that OAuth session, a grant's id — what revoking the grant revokes. The
  fakes in `paladintest` and `paladin.testing` filter on it too.
- The console's grant panel on a user's page links to those capabilities;
  the capability list reads the session from `?session=`.

## v41.3.0, sdk/go/v0.127.0, sdk/python/v0.128.0 — a capability shows the OAuth grant it was exchanged under

- `Capability` carries `session` (the OAuth grant's id), `on_behalf_of` (the
  user the subject acts for) and `client_id`, output only; `List` and `Get`
  set `session`.
- The console's capability list and detail link a delegated capability to
  its grant on the user's page.

## v41.3.0 — deleting an OAuth client revokes its capabilities

- `OAuthClientService.DeleteOAuthClient` now revokes every capability
  exchanged under the client, in every tenant, and releases their holds.
  Before, they stayed usable until their tokens expired.
- Deleting a client already gone still runs the revocation before answering
  `NOT_FOUND`, so a retry finishes a delete whose revocation failed; that
  failure answers `UNAVAILABLE`.
- Revoking a grant's session, from the console or by the client, releases
  the revoked capabilities' holds at once instead of when they expire.

## v41.3.0, sdk/go/v0.127.0 — limes v1.0.0-rc.24

- pgstore v0.52.1 adds four migrations, applied at startup:
  - `027` rebuilds the client and session indexes to span tenants
    (`CREATE INDEX CONCURRENTLY`);
  - `028` replaces the revocation notify trigger, sending a large
    revocation in parts;
  - `029` adds `limes.capabilities.run_id` and backfills it from each
    capability's agent principal — a full pass over the table;
  - `030` indexes it (`CREATE INDEX CONCURRENTLY`).

## v41.3.0 — revoking a connected application stops its capabilities

- Revoking an OAuth grant from the console (`UserService.RevokeOAuthGrant`)
  now also revokes every capability exchanged under the grant's session, as
  the client's own `/oauth/revoke` already did.
- Revoking a grant that is already revoked succeeds instead of answering
  `NOT_FOUND`, so a call that failed part-way is finished by a retry. A grant
  that is not the user's is still `NOT_FOUND`.
- If the capabilities could not be revoked, the call answers `UNAVAILABLE`;
  retry it.

## v41.3.0 — the admin API answers capability errors by limes code

- A capability or tenant-budget store failing under the admin API is
  `UNAVAILABLE`, worth a retry, not `INTERNAL`. The caller is told only to
  retry; the store's error is logged as the failure's `cause`.
- A limes refusal the admin API answers — a delegation wider than its
  parent, a revoked or expired parent — carries the capability reason
  (`ERROR_REASON_CAPABILITY_*`) it carries when verified.

## v41.2.0 — verifying and metering a capability answer alike

- A spent request or copy limit refused while verifying a capability is
  `RESOURCE_EXHAUSTED`, not `PERMISSION_DENIED`, as it already was while
  metering.
- The server's own fault while verifying a capability is `INTERNAL`, not
  `UNAVAILABLE`, and is no longer worth retrying.
- A server-side metering failure no longer tells the caller the store's
  error text; the cause is logged.

## v41.2.0 — metering errors answer with what a retry can mend

- A metering error that is the server's own fault — a misconfiguration, a
  call scoped to the wrong tenant, a broken invariant — is now `INTERNAL`,
  not `UNAVAILABLE`, so clients stop retrying it.
- A refusal of the capability met while metering is `PERMISSION_DENIED`, as
  the same refusal is when the capability is verified, and a record that is
  already there is `ALREADY_EXISTS`.
- `UNAVAILABLE` is left for a failing dependency — the store, the keys, the
  revocation upstream — which a retry may find recovered.

## v41.2.0 — ending an OAuth grant revokes the capabilities it was exchanged for

- **Migration time.** pgstore adds `client_id`, `on_behalf_of` and
  `session` columns to `limes.capabilities`, fills the first two from every
  existing record in one `UPDATE`, and builds four indexes `CONCURRENTLY`. On
  a large table the migration takes longer, and the `UPDATE` holds the rows
  it rewrites while it runs.
- **OAuth access tokens carry `sid`**, the grant's refresh-token family.
  Revoking a grant (`/oauth/revoke`), or a replayed refresh token revoking
  its family, now revokes the capabilities the grant's tokens were exchanged
  for at once.
- Capabilities issued before the upgrade record no session, so ending a
  session does not reach them; they expire with their token as before.

## v41.2.0 — the MCP exchange issues one capability per token

- **A paladin-mcp token is exchanged for one capability**, whichever replica
  exchanges it, and that capability lives as long as the token. Revoking it
  now also refuses the token's later exchanges (`invalid_grant`) until the
  agent's client fetches a new token, where before the next exchange issued
  a fresh capability. Nothing to configure.

## v41.1.0, sdk/go/v0.126.1, sdk/python/v0.127.1 — limes v1.0.0-rc.14: a capability no request can use is refused

- **`max_requests` is at least 1.** `CapabilityService.Issue` and
  `Delegate` refuse `max_requests: 0` as `INVALID_ARGUMENT`: a capability
  that may make no request can do nothing, and limes no longer issues one.
  To stop a capability, revoke it; to bound it, set a limit of 1 or more, or
  `unlimited_requests`. Capabilities issued before the upgrade keep their
  ceilings. The console's issue dialog asks for at least 1.
- A Biscuit copy's own `max_requests` may still be 0.

## v41.1.0 — the MCP server acts through a capability that acts for its user

- **The token-exchange grant issues a capability** (ADR-0041). `/oauth/token`
  with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` answers
  with one limes capability for every plane the user may hold
  (`issued_token_type` `https://github.com/oleg-tkachuk/limes/exchange#capability`),
  no longer with an IAM access token per plane. `audience` names planes by
  their labels, `data`, `admin` and `iam`, and may repeat. The MCP server
  is its only client and moves with it; nothing to do there.
- **The grant needs `capability.enabled`.** A seed client with
  `token_exchange: true` and the capability subsystem off refuses to start.
- **An agent session is revocable on its own.** Each MCP caller's calls
  carry a capability issued under `<capability.issuer_name>/mcp`; revoking
  it stops the session without signing the user out. The audit log's
  `relayed_by` still names the MCP server's exchange client.
- **A user's roles and status apply at the next call.** The planes read
  the user behind a delegated capability on every call, so a role removed
  or a user disabled stops their agents at once.
- **A tainted object is closed to MCP agents.** The delegated capability
  has no `allow_tainted_read`, so a read of a flagged object through the
  MCP server is refused, as for any agent's capability; under the plane
  tokens it was not. A person reads it through their own session.
- **A caller that sends its own capability keeps it.** The MCP edge then
  forwards that capability alone; it no longer forwards the caller's
  `paladin-mcp` token, which no plane accepts.

## v41.0.0, sdk/go/v0.126.0, sdk/python/v0.127.0 — limes v1.0.0-rc.11

- **Every capability is reissued.** limes issues one token format, an RFC
  9068 access token (`typ: at+jwt`), and refuses the earlier
  `limes-cap-v6+jwt` tokens and their Biscuits. Capabilities issued before
  the upgrade stop working; issue them again.
- **`capability.signing_key_kid` names a `limes:` kid.** limes signs and
  verifies only under a kid in its namespace; a set kid without the prefix
  stops the server at startup. The kid derived when it is empty already has
  it.
- **A capability names its OAuth client** (RFC 9068 `client_id`): the
  client the issuer acts for, or `paladin` when it acts for none.
- **Migrations of limes's own schema** (pgstore v0.42) run with the server's;
  nothing to do.

## v40.3.0 — policy history names older writers, and agents can read it

- **Migration `103`** fills in `written_by_tenant_id` for the versions
  written before `102` whose `written_by` is a user's id; a writer that is no
  user stays without one. Nothing to do.
- **MCP tool `paladin_list_policy_versions`** reads a policy layer's
  history, page by page. It matches `paladin_list_*`, so the `read_only` and
  `agent_safe` profiles carry it. Additive.

## v40.3.0, sdk/go/v0.125.0, sdk/python/v0.126.0 — a policy version names its writer's tenant

- **`PolicyVersion.written_by_tenant_id`**: the writer's own tenant, in which
  `written_by` names a user — a platform admin editing another tenant's
  policy is recorded under the platform tenant. Absent with `written_by`, and
  for versions written before it was recorded. Additive.
- **Migration `102`** adds the column and records it; the pool sets
  `paladin.actor_tenant` beside `paladin.actor`.

## v40.3.0, sdk/go/v0.124.0, sdk/python/v0.125.0 — a policy layer keeps every text it has held

- **`PolicyService.ListPolicyVersions`** pages through one layer's texts,
  newest first (`PolicyVersion`: `version`, `cedar_policy`, `written_by`,
  `written_at`). The layer is named as `storageBackends/{id}`,
  `storageBackends/{id}/buckets/{name}`, `tenants/{id}` or
  `tenants/{id}/collections/{name}`; gated as `GetEffectivePolicy` is.
  Additive.
- **Migration `101`** adds `policy_versions` and the triggers that fill it
  on every policy write, and seeds version 1 from each layer that already
  holds a policy. `written_by` is the writer's subject, absent for a write
  no request made — the seeded versions among them.
- The pool now sets `paladin.actor` on every connection it hands out; a
  tool connecting with its own pool records no writer.

## v40.2.0, sdk/go/v0.123.0, sdk/python/v0.124.0 — the object listing narrows and counts by state and content category

- **`ListObjectsRequest` gains `facets` and `count_facets`.** `facets`
  (`ObjectFacets`) narrows the listing to the states and the content
  categories (`ObjectContentCategory`: image, video, audio, text, PDF, JSON,
  other) it names, as conjuncts of `filter`. With `count_facets` the first
  page's answer carries `facet_counts` for facets `state` and
  `content_category`, each counted without its own choices, and
  `facet_counts_exact`, false when the count stopped at its scan cap of
  10 000 objects. No counts come back for a collection whose policies decide
  object by object. Additive.
- The SDK fakes refuse `facets` and `count_facets` with `UNIMPLEMENTED`, as
  they do `filter`.

## v40.2.0, sdk/go/v0.122.0, sdk/python/v0.123.0 — the API says what each capability operation unlocks

- **`CapabilityService.ListOperationGrants`** returns a row for each
  operation a capability's caveats can name, with its `basis`
  (`CapabilityGrantBasis`): `BUILTIN` with the Cedar `actions` the built-in
  policy grants in the capability's own tenant; `TENANT_POLICY` for `manage`,
  which needs a tenant policy; `PAIRED` for `object:presign`, asserted beside
  the `paired_ops` that grant the action; `CAVEATS_ONLY` for `share`, whose
  `procedures` (`Delegate`) ask no policy. Gated on `ReadCapability`.
  Additive.
- The console shows it when a capability is issued and on its record.

## v40.2.0 — a capability does what its caveats allow without a tenant policy

- **A capability is now permitted, in its own tenant, the data-plane actions
  of the operation its caveats admitted** (ADR-0038): `get`/`list` →
  `GetObject`, `PresignGet`; `put` → `PutObject`, `PresignPut`,
  `UpdateObject`, `CopyObject`; `object:tag` → `UpdateObject`. Before, a tenant with no stored policy
  refused these with "denied by policy". A tenant that relied on that
  default to keep issued capabilities inert must add a `forbid`, for example
  `forbid (principal, action, resource) when { principal has kind && principal.kind == "capability" };`.
- **Cedar context gains `capability_op`** (String): the operation a
  capability's caveats admitted for the request, `""` without one. Tenant
  policies may read it. Additive.

## v40.2.0, sdk/go/v0.120.0, sdk/python/v0.121.0 — the configuration's defaults beside the running one

- **`GetConfigResponse` gains `defaults_yaml`**: the configuration a
  deployment runs on where it sets nothing, in the running one's shape and
  redaction. Additive.
- The console's `/config` Compare opens on the defaults, so an operator sees
  every setting the deployment changed; a pasted `config.yaml` is one click
  away.

## v40.1.0, sdk/go/v0.119.0, sdk/python/v0.120.0 — the audit log narrows and counts by plane, outcome and service

- **`ListAuditLogRequest` gains `facets` and `count_facets`**. `facets`
  (`AuditFacets`) narrows the log to the planes (`actor_audience`), outcomes
  (`AuditOutcome`) and services it names; with `count_facets` the first
  page's answer carries `facet_counts`, each facet counted without its own
  choices. An entry's service is its action's `pkg.Service`, or the action
  itself for one of the server's own. Additive.
- **Migration `100`** replaces `list_audit_entry_ids` with a version taking
  the facets and adds `audit_service`; nothing to do.
- The console's audit log filters by plane, outcome and service with counts.

## v40.0.0, sdk/go/v0.118.0, sdk/python/v0.119.0 — limes v1.0.0-rc.4

- **`CapabilityServiceRevokeBiscuitResponse` gains `already_revoked` and
  `revocation`**, as the capability's own revoke answers: a second revoke of
  a copy keeps the first entry and says so. Additive.
- **The capability metrics limes exports are renamed** (otelmetric
  v0.8–v0.9): the attributes `outcome`, `unit_code` and `tenant_id` are
  `limes.outcome`, `limes.unit_code` and `limes.tenant_id`; the revocation
  cache's `kind` is `limes.revocation_cache.kind`, `…clears_revoked` is
  `…narrow_clears` and `…last_sync_loss` is `…last_sync_loss_time`. Update
  queries, dashboards and alerts that read them.
- **`capability.revocation_cache_ttl` or `revocation_cache_synced_ttl`
  above 24 hours is refused at startup**: limes now caps how long its
  revocation cache trusts an answer, and caches for the longer of the two.
- **A capability listing's page token is bound to its tenant**: one handed
  back for another tenant is refused. A token issued before the upgrade is
  refused once; start again from the first page.
- **`paladin_capability_charges_total` is gone**: limes counts every charge
  attempt itself, as `limes_charge_decisions_total` with `limes_outcome`
  (`allowed`, `budget_exceeded`, `tenant_budget_exceeded`, …, `error`) and
  `limes_tenant_id`. The chart's `PaladinCapabilityChargesRefused` and the
  bundled dashboard read it; `capabilityChargesRefused.refusedOutcomes`
  defaults to `budget_exceeded` and `tenant_budget_exceeded`, so an override
  naming the old outcomes needs the new ones. A charge in another currency
  is `error` there, and no longer fires the alert. Point your own queries and
  alerts at the new series; `paladin_capability_charge_amount` stays.
- **The ledger's old charges can be removed**: with
  `worker.jobs.capability.purge_charges: true` the capability purger removes
  the charges taken more than `charges_older_than` ago (default and minimum
  9624h, 401 days), as limes's ledger housekeeping does. Off by default; a
  removed charge leaves every billing sum.

## v39.4.0, sdk/go/v0.117.0, sdk/python/v0.118.0 — operation listings narrow and count by state and type

- **`ListOperationsRequest` gains `facets` and `count_facets`** on both the
  admin and the data plane. `facets` (`OperationFacets`) narrows the listing
  to the states and types it names; with `count_facets` the first page's
  answer carries `facet_counts` (`FacetCount`): for facet `state` each
  `OperationState`'s name, for `type` each type, each facet counted without
  its own choices. Additive; a caller that sets neither sees no change.
- The console's operations page filters by state and type with counts in
  place of its single-choice state filter.

## v39.4.0 — the data and IAM planes record what an action changed

- **An audit entry of the data or IAM plane carries `state_before_json` and
  `state_after_json`**, as the admin plane's does: the object, its lock or
  its tags around an object action, the user or the user's settings around
  an IAM one, read through their own Get and redacted. A batch records its
  request alone: its operation records what each object came to.
- Each audited action that records no state says why in
  `backend/internal/app/audit_state_test.go`.

## v39.4.0, sdk/go/v0.116.0, sdk/python/v0.117.0 — an audit entry records the resource before and after

- **`AuditLogEntry` carries `state_before_json` and `state_after_json`**:
  the resource an admin action changed, as its own Get read it just before
  and just after the action, secret fields redacted. A create records only
  the after — its answer — and a delete only the before. The reads are not in
  the action's transaction, so a concurrent change can fall between them.
  The deprecated `before_json` and `after_json` stay empty.
- **Migration `099`** adds the two columns to `audit_log`; nothing to do.
- The console's audit entry gains a Changes tab listing the fields that
  changed.

<!-- end of sections -->

## v39.4.0, sdk/go/v0.115.1, sdk/python/v0.116.1 — the Python fake refuses an unknown capability as the server does

- **`paladin.testing` under `strict_auth` refuses a capability it did not
  issue with `PERMISSION_DENIED` and reason `CAPABILITY_INVALID_TOKEN`**, as
  the server and `paladintest` do, rather than `capability: invalid
  signature` with no reason. A test that matched the old message must match
  the reason instead; the message stays the fake's own.

## v39.4.0, sdk/go/v0.115.0, sdk/python/v0.116.0 — an operation names a copy's key prefix and a tag update's tags

- **`OperationTarget` carries `destination_key_prefix` and `tags`**: what
  a copy and a tag update asked for, beside the collections and `replace`
  it carried already, so a retry of the objects an operation could not act
  on asks for the same thing. The console's Retry failed now offers it for
  both. Nothing else changes.

## v39.4.0, sdk/go/v0.115.0, sdk/python/v0.116.0 — preview what a tenant purge removes

- **`TenantService.PreviewPurgeTenant`** answers, changing nothing, what
  `PurgeTenant` would remove with a tenant — its API tokens and event
  subscriptions by name (the first 50 of each, with counts of all), how many
  capabilities — and what would refuse it: one `PurgeBlocker` for each kind
  of row the tenant still owns (collections, buckets, users). It asks what
  `PurgeTenant` asks of the caller, and reads an active tenant as well as a
  trashed one. Nothing else changes.

## v39.4.0, sdk/go/v0.114.0, sdk/python/v0.115.0 — the fakes seal a real Biscuit

- **`paladin.testing`'s Biscuit is a real one with the `biscuit` extra**,
  as `paladintest`'s is, so `paladin.attenuate` narrows it and a narrowed
  copy under `strict_auth` is held to its narrowing as the server holds it.
  Without the extra it stays a stand-in.
- **`paladintest`'s Biscuit is a real one**, so `paladin.Attenuate` narrows
  it; a narrowed copy presented under `WithStrictAuth` is held to its
  narrowing as the server holds it. A test that compared the Biscuit with
  a fixed prefix must stop.
- **Under `WithStrictAuth` a capability the fake did not issue is refused
  in the verifier's words**, as the server refuses it — `PermissionDenied`
  with an `ErrorInfo` reason such as `CAPABILITY_INVALID_TOKEN` — rather
  than `capability: invalid signature`, which the server never sent.

## v39.4.0, sdk/go/v0.113.0, sdk/python/v0.114.0 — the fakes name a release on every response

- **`paladintest` and `paladin.testing` set `X-Paladin-Version` on every
  response**, as the server does, so an error from either fake carries a
  server version: `paladintest.ServerVersion` on `*paladin.Error`'s
  `ServerVersion`, `paladin.testing.SERVER_VERSION` on `PaladinError`'s
  `server_version`. A consumer test that reports a contract skew can now
  run against the fake. Nothing else changes.

## sdk/python/v0.113.0 — the Python fake holds a capability to its caveats

**Python SDK:** the first release that carries this section.

- **`paladin.testing` under `strict_auth=True` checks a capability its
  `CapabilityService` issued or delegated as the server does**, as the Go
  fake already does: the plane its audience names, its request ceiling and
  its ancestors', and each call's operation and resource. The refusals carry
  the server's code, `ErrorInfo` reason and message, held to the server's by
  `sdk/testdata/capability_refusals.json`.
- **A consumer test may now fail for a real reason.** One that issued a
  capability for `paladin.AUDIENCE_DATA` (`paladin-data`) rather than
  `Plane.DATA` (`data`), or that calls outside the caveats it issued, passed
  before and fails on the cluster; it now fails in the test too. Fix the
  capability, not the test.

## v39.4.0, sdk/go/v0.112.0, sdk/python/v0.112.0 — a platform admin reads another tenant's operations

**Server:** the first release that carries this section. **SDKs:** the first
release that carries it.

- **`PlatformOperationService.ListOperations` takes `tenant_id`**: the tenant
  whose operations to list, as `ListAuditLogRequest.tenant_id` names one.
  Empty is the caller's own, as before. A caller other than a platform admin
  that names another tenant is refused with `PermissionDenied`.
- **The console's tenant History** names the tenant, so a platform admin on
  another tenant's page sees the batches its collections ran, which it did
  not before.

Additive: no action needed.

## v39.4.0, sdk/go/v0.112.0 — the Go fake holds a capability to its caveats

**Go SDK:** the first release that carries this section.

- **`paladintest` under `WithStrictAuth` checks a capability its
  `CapabilityService` issued or delegated as the server does**: the plane its
  audience names, its request ceiling and its ancestors', and each call's
  operation and resource. The refusals carry the server's code, `ErrorInfo`
  reason and message, and a backend test holds the fake to them.
- **A consumer test may now fail for a real reason.** One that issued a
  capability for a bearer audience (`paladin.AudienceData`, `paladin-data`)
  rather than a plane's label (`paladin.PlaneNames(paladin.PlaneData)`), or
  that calls outside the caveats it issued, passed before and fails on the
  cluster; it now fails in the test too. Fix the capability, not the test.
- A capability from `IssueCapability(tenant)` carries no caveats and is
  checked as before. The budget is not counted, as the server counts none
  unless a price per request is set.

## v39.4.0, sdk/go/v0.112.0 — limes v1.0.0-rc.1: a revoke is written whatever the capability's state

**Server:** the first release that carries this section.

- **limes v1.0.0-rc.1, pgstore v0.35.1, otelmetric v0.6.2.** The candidate is
  limes v0.117.0's code under the next major; nothing in its API changed.
- **Migrations `019` and `020`** run at startup, as pgstore's do. `019` has
  `limes.is_revoked` answer from `limes.revocation_lineage`'s walk, so the two
  never disagree; `020` recreates `revocation_lineage` with a `known` column.
  The `postgres-limes-schema` health row now wants `20`. Deploy the schema
  with the binary that needs it, as before.
- **A capability not on record is not cached.** `RevocationLineage` reports
  it as not found, and the revocation cache keeps no answer for it, which no
  ancestor's revocation could clear.
- **A revoke writes its entry whatever the capability's state**, and pgstore
  reads a revoked capability's state for `before` under its row lock.
- **The revocation cache holds the synced TTL only while every revocation
  signal listens**, and pgstore's watcher now notices a connection that died
  without an error. Paladin runs one watcher per replica, so the rule in
  ADR-0037 is unchanged.

Additive: no action needed beyond deploying the migrations with the binary.

## v39.3.0, sdk/go/v0.111.0, sdk/python/v0.111.0 — limes v0.116.0: a revoke says what the capability was, and the cache follows its watcher

**Server:** the first release that carries this section.

- **`CapabilityService.Revoke` answers `before`**: the capability's
  disposition just before the call, by the rules `disposition` is read by.
  Only a revoke of an `ACTIVE` one stopped something new; one already
  revoked through an ancestor, or expired, still gets its entry for the
  audit, and answers `REVOKED` or `EXPIRED` with `already_revoked` false.
  The console says which.
- **`capability.revocation_cache_synced_ttl`** (default `30s`) is how long a
  revocation answer is cached while the revocation watcher listens, and every
  revocation clears what it outdates at once. `capability.revocation_cache_ttl`
  (default `2s`) bounds it before the watcher's first `LISTEN` and while it
  reconnects: losing the connection cuts every cached answer back to it at
  once. A synced TTL shorter than `revocation_cache_ttl` is raised to it, so
  a deployment that set the TTL above `30s` keeps it for both. A negative
  `revocation_cache_ttl` is refused by the config schema; the old comment
  that one disables the cache was wrong, as limes refuses it at startup.
- **The health page has a `capability-revocations` row**: unhealthy, never
  critical, while the watcher does not listen, with when it last lost its
  connection, and how often it has connected, lost its connection and passed
  a notification on.
- **The revocation cache's metrics gain** `limes.revocation_cache.clears_revoked`,
  `.clears_without_lineage`, `.lineage`, `.synced`, `.syncs`, `.syncs_lost`,
  `.last_sync_loss`, `.signals` and `.signals_whole`. `synced` at 0, or
  `syncs_lost` growing with `syncs`, is the watcher losing its connection.
- **limes v0.116.0, pgstore v0.35.0, otelmetric v0.6.0.** No migration. The
  cache is built with `RequireLineage`, so a decorator of the capability
  store that drops `RevocationLineage` fails startup rather than turn every
  revocation into a whole clear.

Additive: no action needed.

## v39.3.0, sdk/go/v0.110.0, sdk/python/v0.110.0 — a revoke says what it did, and a capability what stopped it

**Server:** the first release that carries this section.

- **`CapabilityService.Revoke` answers `already_revoked` and `revocation`**:
  whether the capability had an entry of its own before the call, and the
  entry that stands — the first one, which a second revoke keeps. One stopped
  only through an ancestor had none, so its first revoke writes it.
- **`CapabilityService.Get` answers `stopped_by`** for a revoked capability:
  its own entry, or that of its nearest revoked ancestor, with that
  capability's id. `revocation` keeps its meaning: the capability's own entry.
- **limes v0.114.0, pgstore v0.33.0.** Migration `018` names the revoked
  capabilities in pgstore's notification, so a revocation clears only the
  cached answers it outdates rather than every replica's whole cache. A
  revoke of a capability whose parent is revoked already sends none, since
  nothing cached of it can be stale. Migration `014` now builds its indexes
  concurrently; a store that ran it before is unaffected.

Additive: no action needed.

## v39.3.0, sdk/go/v0.109.0, sdk/python/v0.109.0 — the platform's spend over time

**Server:** the first release that carries this section.

`BillingService.GetPlatformTimeSeries` is `GetTenantTimeSeries` over every
tenant's charges: what the platform was charged, a bucket at a time, in the
unit most of the period's charges are in. It reads every tenant's rows, so
it is gated as `ListTenantSpend` is: `ReadBilling` and the `platform.admin`
role. The console's Billing page charts it. Additive: no action needed.

## v39.3.0 — limes v0.112.0: a page token keeps its filters

**Server:** the first release that carries this section.

- **A `CapabilityService.List` page token is refused, as `INVALID_ARGUMENT`,
  when it comes back with another `principal_kind`, `subject` or
  `dispositions`** than the page that handed it out, as one of the other
  `sort_order` already was. Start over from the first page when a filter
  changes.
- **limes migrations `015`–`017`** keep a revoked flag on each capability
  record: 015 adds the column, a change of the catalog that rewrites no row;
  016 marks the records revoked before it; 017 builds its indexes
  `CONCURRENTLY`. A revoke now also updates the rows of the records below the
  one revoked.

## v39.3.0, sdk/go/v0.108.0, sdk/python/v0.108.0 — an operation says how many objects it left behind

**Server:** the first release that carries this section.

A batch that runs to its end is `SUCCEEDED` whatever it could not do, so a
`done` operation could hold failures that only its response named.

- **`Operation.failed_count`** (admin 16, data 15) is how many objects the
  batch could not act on, each with its reason in the response's failures;
  0 for a clean batch and for any other operation.
- **The operations filter takes `failed_count` and `has_failures`**: failed,
  or a batch that left objects behind. `has_failures` is pushed into the
  query, so a list of them pages through them alone.
- **Migrations `096`–`097`** add the column, a change of the catalog alone,
  and count it for the batches that finished before it from their response.
- **A batch tries an object again when another write changed it** between
  its read and its write, up to three times on a fresh read, before it
  reports a version conflict.

## v39.3.0, sdk/go/v0.107.0, sdk/python/v0.107.0 — Get gives a capability its disposition

`CapabilityService.Get` sets `Capability.disposition` (12), by the same rules
as `List`: revoked — itself or through a revoked ancestor — over expired over
active. A capability stopped only through its parent has no `revocation` of
its own, so read `disposition`, not `revocation`, for whether it still works.
Additive: no action needed.

## v39.3.0, sdk/go/v0.106.0, sdk/python/v0.106.0 — a tenant's capabilities list without a principal

**Server:** the first release that carries this section; an older one still
requires `principal_kind` and `subject` and answers as before.

`CapabilityService.List` pages through a tenant's capabilities, read from
limes's record listing (limes v0.111.0, pgstore v0.31.0):

- **`principal_kind` and `subject` are optional filters.** A request with the
  tenant alone lists all of it.
- **`dispositions` (8)** lists the capabilities of any of active, expired and
  revoked, by the same rules as the platform's capability counts; revoked
  includes one stopped through a revoked ancestor. `include_expired` and
  `include_revoked` are deprecated: a request naming no dispositions lists the
  active ones and those the flags add, as before — except that an active
  capability under a revoked ancestor now counts as revoked, not active.
- **`sort_order` (9)**: `SORT_ORDER_DESC` lists newest first; unspecified is
  oldest first, as before. A page token holds the order it was handed out
  for, and one from before this release is refused as `INVALID_ARGUMENT`.
- **`Capability.disposition` (12)** is each listed capability's disposition;
  the other RPCs leave it unspecified.
- **limes migration `014`** adds the indexes the listing reads. It builds them
  without `CONCURRENTLY`, so on a large `limes.capabilities` the first start
  of this release holds writes to it until they are built.

## v39.3.0, sdk/go/v0.105.0, sdk/python/v0.105.0 — a collection says what it holds

`Collection.usage` (14) is what a collection holds now — its available
objects' count and bytes, and when the newest was stored — on `GetCollection`
and `ListCollections`; objects still uploading or in the trash are not in it.
Migration `095` adds the index it is read from, concurrently. The console's
collection lists show it. Additive: no action needed.

## v39.3.0, sdk/go/v0.104.0, sdk/python/v0.105.0 — an API token can be read by its name

`APITokenService.Get` reads one token's metadata by its resource name,
`tenants/{tenant}/apiTokens/{id}`, under the same authorisation as `List`.
The console uses it to name an audit entry's `apikey:<id>` actor. Additive: no
action needed.

## v39.3.0, sdk/go/v0.103.0, sdk/python/v0.104.0 — an audit entry records its request, not before and after

**Server:** v39.3.0 or later; so far released as v39.3.0-rc.1, so a v39.2.x server still answers as before.

`AuditLogEntry.before_json` and `after_json` were documented as the
resource's state before and after an action. Nothing recorded the state:
`before_json` was always empty, and `after_json` held what the action was
asked to do — an RPC's request with secrets redacted, an OAuth endpoint's, or
the storage event behind an ingest.

- **`request_json` (16) carries it**, and both old fields are deprecated and
  no longer set. Read `request_json` where you read `after_json`.
- **Migration `094`** renames `audit_log.after_json` to `request_json` and
  drops `before_json`; both are catalog changes only.
- **`ExportAuditLog`'s entries name it `request`**, in place of `before` and
  `after`.
- **The console's audit entry has a Request tab** in place of Changes and
  Snapshots, and the summary's request id is labelled *Request id*.

## sdk/python/v0.103.0 — the Python SDK has tags of its own

The Python SDK releases as `sdk/python/vX.Y.Z`, versioned by its own commits
and those to `proto/` and `sdk/testdata/`, rather than by the Go SDK's tag.
Both share `0.102.0`; from there each moves when its own code does, and the
numbers say nothing of each other: `sdk/go/v0.103.0` and `sdk/python/v0.104.0`
were cut from the same commit, and a Go and a Python release of one number need
not carry the same change. Move a
pin from `@sdk/go/vX.Y.Z#subdirectory=sdk/python` to
`@sdk/python/vX.Y.Z#subdirectory=sdk/python`. A pin to a Go tag keeps
installing what it pointed at, but no later Python release has one.

## v39.3.0, sdk/go/v0.102.0, sdk/python/v0.102.0 — the SDKs type the planes; refusals no longer name limes

The SDK changes are in these tags. The refusals' text is the server's:
**Server:** v39.3.0 or later; so far released as v39.3.0-rc.1, so a v39.2.x server still answers as before.

- **`Attenuation.Planes` takes `[]paladin.Plane`** (Go), and Python's
  `attenuate(planes=…)` takes `Plane` members. A plane's label — `PlaneData`,
  `"data"` — is not its bearer audience, `AudienceData` (`"paladin-data"`); a
  capability issued for the audience is refused as an audience mismatch.
  `Plane.Audience()` (Go) and `Plane.audience` (Python) map one to the other,
  and `PlaneNames` spells planes for `CapabilityServiceIssueRequest.audience`.
  Replace `[]string{"data"}` with `[]paladin.Plane{paladin.PlaneData}`.
- **`TokenOf`** (Go) and **`token_of`** (Python) return the token an Issue or
  Delegate answer carries, for a caller that presents the capability as
  issued; `CredentialOf` still prefers the Biscuit.
- **`UnlimitedCaveats(ops...)`** and **`ValidateIssueCaveats`** (Go),
  `unlimited_caveats` and `validate_issue_caveats` (Python): caveats with
  both ceilings set, and a check before the call of what Issue refuses in
  their shape — no operation, an operation the server does not know, a
  request or budget ceiling left unset. It checks no more than that: not the
  resources, where an empty `resource_prefixes` and `resource_uris` grant
  every resource of the tenant rather than none, nor anything Issue decides
  by the caller or the tenant. The unlimited budget is in the platform's
  currency (`capability.currency`); a capability that is charged in another
  unit sets `unlimited_budget.currency_code`, and one that is never charged
  is unaffected.
- **A `Credential` logs by name.** Go's `CredentialForm` writes `"token"` or
  `"biscuit"` through slog and encoding/json; `Credential` logs its form and
  `biscuit_status` and never its value. Python's `Credential` leaves the
  value out of its repr.
- **Refusals no longer begin with `limes: `.** `capability … not found` and
  `invalid caveats: …` read the same without the name of the server's
  capability library, from the server, both fakes and `Op.Validate`. A client
  matching on the old text matches on the rest of it, or better, on the
  error's reason.

The Go SDK still depends on limes, for `Attenuate` and `Op.Validate`; no
exported type of it names one, so a client never imports it.

## v39.3.0, sdk/go/v0.101.0 — the audit log sorts by client

**Server:** v39.3.0 or later; so far released as v39.3.0-rc.1, so a v39.2.x server still answers as before.

`ListAuditLog` takes `order_by: "user_agent"` (migration `093`); a call that
sent no User-Agent sorts as empty. The console's Client column sorts by it.
Additive: no action needed.

## v39.3.0, sdk/go/v0.100.0 — the audit log names the client a call came from

Migration `092` adds `audit_log.user_agent`, and `AuditLogEntry` gains
`user_agent`: the User-Agent the call arrived with, cut to 256 bytes. The
SDKs send their own name and version there, followed by the application's,
which it sets with `paladin.WithUserAgentSuffix` (Go) or `user_agent_suffix`
(Python). It is filterable and searchable, exported and mirrored, and the
console shows it as the Client column. Additive: no action needed.

## sdk/go/v0.97.0 — a capability has a Biscuit without share

limes v0.110 seals a Biscuit for any capability: a copy narrowed from it
allows no more than the token, which its bearer could hand on as it is, and a
copy with limits of its own is counted against `copy_ceiling`.

- `CapabilityService.Issue` and `Delegate` return `biscuit` whatever the
  capability's ops; only a key-bound capability (`KEY_BOUND`) and one not
  valid yet (`NOT_YET_VALID`) still come back without one.
- `BISCUIT_STATUS_NOT_SHAREABLE` is deprecated and never sent, and
  `require_biscuit` no longer refuses a capability for lacking `share`.
- `Delegate` still requires `share` on the parent.
- `paladin.CredentialOf` (Go) and `credential_of` (Python) say what to
  present for an issued capability: the Biscuit when it was issued, the token
  otherwise, with the form and the status. A client that only presents the
  capability should use them rather than read `biscuit`; `BiscuitOf` stays
  for one that must narrow it.
- Go's `paladintest` and Python's `paladin.testing` serve
  `CapabilityService` — `Issue`, `Delegate`, `Revoke`, `Get` — to the
  server's rules: a Biscuit unless key-bound or not
  yet valid, `InvalidArgument` for a bare `presign`, `require_biscuit` and
  `Delegate`'s `share` as the server answers them. A consumer's own fake of
  the service can go.
- The operations are typed. Go: `paladin.Op`, with `Attenuation.Ops []Op`,
  `paladin.OpNames(…)` for `CapabilityCaveats.ops`, and `Op.Validate`. Python:
  `paladin.Op`, a `StrEnum`, whose members `OP_GET` … `OP_OBJECT_TAG` now are;
  `attenuate` raises `ValueError` for an op that is no member.

## sdk/go/v0.96.0 — the SDKs need no limes

A client works with the Paladin SDK alone; limes is the server's.

- Go: `paladin.Attenuate(biscuit, paladin.Attenuation{…})` narrows a
  capability's Biscuit offline, in place of `limes.Attenuate`. Its ceilings
  are `MaxRequests` and `MaxBudgetNanos` (`*int64`), the plane list is
  `Planes`, and the key binding is `BindJKT`, as Python's `attenuate` names
  them; a token that is no Paladin Biscuit wraps `paladin.ErrInvalidArgument`.
- Both SDKs name every operation a capability can carry: Go's `paladin.OpGet`
  … `paladin.OpObjectTag`, Python's `OP_GET` … `OP_OBJECT_TAG`, in place of
  limes's `OpGet` and the rest. A client that imported limes for them drops
  the import.

## sdk/go/v0.95.0 — an Issue answer says why it carries no Biscuit

Since limes v0.109 a capability is sealed as a Biscuit only when its ops
include `share`, and `biscuit` came back empty with nothing else to say so: a
client that handed the empty field on looked authorized and was not.

- `CapabilityServiceIssueResponse.biscuit_status` names whether `biscuit` is
  set — `ISSUED` — or why not: `NOT_SHAREABLE`, `KEY_BOUND`,
  `NOT_YET_VALID`. Issue and Delegate both answer it.
- `require_biscuit` on `CapabilityServiceIssueRequest` and
  `CapabilityServiceDelegateRequest` refuses such a capability with
  `FailedPrecondition`, naming why, before anything is issued.
- The SDKs read it for you: Go's `paladin.BiscuitOf` returns an error
  wrapping `paladin.ErrNoBiscuit`, Python's `biscuit_of` raises
  `NoBiscuitError`. A client that needs the Biscuit should use one of them,
  or set `require_biscuit`, rather than read `biscuit` directly.

## v39.0.0, sdk/go/v0.93.0 — capabilities run on limes v0.109

The capability layer moves from limes v0.69.0 to v0.109.0 and pgstore from
v0.11.1 to v0.30.1. What an operator or a client sees:

- **Every capability is reissued.** A verifier accepts `limes-cap-v6+jwt`
  alone; a token issued before the upgrade is refused as invalid. Issue the
  capabilities in flight again once the release is deployed.
- **The data plane's own operations are namespaced.** `presign` is
  `object:presign` and `tag` is `object:tag`; `search` and `embed`, which no
  handler checked, are gone, and a caveat naming any of the four bare names is
  refused. limes defines only `get`, `put`, `list`, `delete`, `share` and
  `manage`.
- **A Biscuit needs `share`.** `CapabilityService.Issue` returns `biscuit`
  only for a capability whose ops include `share`, since a Biscuit is handed
  on offline; the rest get their token alone.
- **Biscuit copies have a quota.** `CapabilityCaveats.copy_ceiling` —
  `max_copies` or `unlimited_copies` — bounds how many copies that set limits
  of their own are counted, across the capability and every one delegated from
  it. Left unset it is unlimited on Issue, within the server's own bound per
  capability, and the parent's on Delegate.
- **`CapabilityPrincipal.parent_agent_id` (13) is `parent_run_id`**, the run
  of the agent a capability was delegated from, which the server sets on
  Delegate. **`Capability.generation` (10) is gone**: nothing read it.
- **A budget names its unit.** limes assumes none for an empty one: a ceiling
  that lets anything be spent is in the platform's currency on Issue, the
  parent's on Delegate, and the tenant's existing unit on a budget update. An
  amount on record in no unit is answered in `XXX`.
- **The database role matters.** pgstore's migrations 009–013 run with the
  migrate step and take EXECUTE on the `limes` schema's functions from every
  role but the runtime ones (`paladin_app`, `paladin_reaper`), which the step
  grants it to.

## sdk/go/v0.92.0 — BatchCopyObjects takes a key prefix, as it always ran

`BatchCopyObjectsRequest.destination_key_template` is now
`destination_key_prefix` (field 4). The field was documented as a CEL
expression over the source object, but the server always used it as a literal
prefix: a caller who sent `"backup/" + key` got that text, quotes and all, in
front of every copied key. The name now says what it does, an empty prefix
keeps each source's key, and the MCP tool `paladin_batch_copy` takes
`destination_key_prefix` too.

The wire number is unchanged, so a binary client that sent a plain prefix
keeps working; a JSON client, and any client that sent an expression, has to
change.

## v39.0.0 — the console names the storage origins it uploads to

The console's Content-Security-Policy allowed only its own origin, so the
browser refused every upload PUT and image preview on a presigned URL, which
points at a storage backend's `public_endpoint`. An upload still created its
record and then never finished.

Set the console chart's `storageOrigins` to those endpoints, as origins:

```yaml
# paladin-console.yaml
storageOrigins:
  - https://s3.example.com
```

The policy is now set at request time by the console's proxy rather than
baked into the image, so a change takes a rollout, not a rebuild. An entry
that is not a bare `http(s)` origin is refused by the chart.

## v38.1.0 — the OAuth endpoints answer an outage as a server error

- `/oauth/authorize`, `/oauth/token` and the token exchange answer
  `server_error` (HTTP 500, or the client's redirect with
  `error=server_error` once its redirect URI is known) when a store or the
  policy engine fails. Before, that read as `invalid_client`, wrong
  credentials, `access_denied` or `invalid_grant`. A client should retry a
  `server_error` rather than re-authorize.
- A code redeemed for a user who moved to another tenant since is refused
  with `invalid_grant`, as a refresh already was.

## v38.1.0 — a logout that could not end the session says so

- `AuthService.Revoke` answers `INTERNAL` when the session's refresh tokens
  could not be revoked, where it answered OK and left the session live. A
  client should treat it as not logged out and retry.

## v38.1.0 — idle self-registered OAuth clients are removed

- **Behaviour.** The worker now removes a client that registered itself
  (RFC 7591) once it is older than `worker.jobs.oauth_client_reap.max_age`
  (default `168h`) and holds no live grant and no redeemable authorization
  code. Seeded clients are never removed. Set
  `worker.jobs.oauth_client_reap.interval: 0` to keep every client.
- Migration 084 adds `oauth_clients.self_registered` and marks the clients
  dynamic registration created before it, by their `paladin-<UUIDv7>` id.
- `OAuthClientService.DeleteOAuthClient` removes a client with every grant
  issued to it, from the console's MCP access page. Platform admins only; a
  client in `auth.oauth.seed_clients` is refused.

## v38.0.0 — dynamic registration follows RFC 7591 and RFC 8252

- **Breaking, behaviour.** An `http` redirect URI is accepted only on a
  loopback host (`localhost`, `127.0.0.1`, `[::1]`), a native client's
  redirect, even with `http` in `auth.oauth.allowed_redirect_schemes`.
- `/oauth/register` reads RFC 7591's space-separated `scope` beside the
  older `scope_list`. A client naming no scope may ask for `paladin.read` and
  `paladin.write`, the user choosing at consent; one naming no audience gets
  `paladin-mcp` when the MCP server's resource URL is configured, else
  `paladin-data`. `audiences` is limited to `paladin-data`, `paladin-admin`
  and `paladin-mcp`.
- The response carries `client_id_issued_at`, `scope`, `grant_types`,
  `response_types` and `token_endpoint_auth_method`.

## v38.0.0 — the token exchange has its own rate limit

`auth.oauth.token_exchange_rate_limit_per_minute` (default 6000) caps the RFC
8693 exchange grant per client, apart from `token_rate_limit_per_minute`, which
keeps guarding the other grants. Every exchange the MCP server makes counts
against its one client, so the shared default of 60 refused new sessions past
about twenty a minute. A deployment that raised `token_rate_limit_per_minute`
for that reason can lower it again.

## v38.0.0 — a user can list and revoke their OAuth grants

`UserService` gains `ListOAuthGrants` and `RevokeOAuthGrant`, and the console
shows them as Connected applications on the profile and on a user's page. A
grant is a live family of OAuth refresh tokens; revoking it stops the client
refreshing at once, and an access token it holds expires within minutes. A user
manages their own grants and a tenant admin those of the tenant's users,
through built-in Cedar permits on the new `ReadOAuthGrants` and
`RevokeOAuthGrant` actions. Additive: no action needed.

## v38.0.0 — the audit log names the MCP server that relayed a call

Migration `083` adds `audit_log.relayed_by`, and `AuditLogEntry` gains
`relayed_by`: the OAuth client named in the `act` claim of an exchanged token
(ADR-0036), so a call an agent made through the MCP server reads as the user's,
via the MCP server. Empty for a direct call. The audit mirror event carries it
too. Additive: no action needed.

## v38.0.0 — the MCP server exchanges the caller's token

[ADR-0036](adr/0036-the-mcp-server-is-its-own-audience.md), behind
`mcp.oauth.exchange`.

- **Breaking, behaviour, once enabled.** Set `mcp.oauth.exchange.client_id`
  and its secret, matching a seed client with `token_exchange: true`, and the
  streamable-HTTP MCP server accepts `paladin-mcp` tokens only: a plane token,
  an API token and `X-Paladin-Token` with either are refused with 401. An
  agent authorizes for `mcp.oauth.resource_url`, and its client's
  `allowed_audiences` must list `paladin-mcp`.
- One session then reaches every plane; stdio is unchanged.
- Without `mcp.oauth.exchange` the MCP server behaves as before.

## v38.0.0 — the OAuth server issues paladin-mcp and exchanges it

[ADR-0036](adr/0036-the-mcp-server-is-its-own-audience.md), the
Authorization Server's half. The MCP server does not use it yet.

- **Breaking, behaviour.** `/oauth/authorize` applies the eligibility a console
  login does: a user without a role in the admin plane's list is refused
  `paladin-admin` with `invalid_target`, where it used to get the token and
  meet Cedar on every call.
- A client asking for `mcp.oauth.resource_url` as its `resource` is issued
  `paladin-mcp`, when its `allowed_audiences` list it.
- A seed client takes `client_secret` or `client_secret_ref`, stored hashed,
  and `token_exchange: true` lets it call the RFC 8693 grant. A confidential
  seed client without a secret, or a public one with one, now fails to load.
  The chart lets the resolver read each `client_secret_ref`.
- IAM serves `/.well-known/oauth-authorization-server` when
  `mcp.oauth.authorization_server.issuer` is set, and the chart routes it.

## v38.0.0 — an OAuth client writes only with `paladin.write`

[ADR-0035](adr/0035-an-oauth-grant-is-bound-and-its-scopes-enforced.md).

- **Breaking, behaviour.** An access token from `/oauth/token` carries the
  `client_id` and `scope` claims, and every plane refuses an OAuth client
  whose grant lacks `paladin.write` any RPC not marked `NO_SIDE_EFFECTS`, with
  `PERMISSION_DENIED`. An agent that writes asks for `paladin.write` and is
  authorized again. Console sessions, API tokens and capabilities are
  unaffected.
- Tenant policies see the grant as `context.oauth_client_id` and
  `context.oauth_scopes` on every call, not only on `AuthorizeOAuth`.

## v38.0.0 — an OAuth refresh token is bound to its grant

[ADR-0035](adr/0035-an-oauth-grant-is-bound-and-its-scopes-enforced.md).

- **Behaviour.** `/oauth/token` redeems a refresh token only for the
  client it was issued to, and re-issues the grant's own audience and scopes
  instead of `paladin-data` with none. A refresh token issued before migration
  082 records no grant and is refused with `invalid_grant`: each OAuth client
  authorizes once more.
- **Behaviour.** `AuthService.RefreshToken` and `ExchangeAudience`
  refuse a refresh token the OAuth Authorization Server issued, and
  `/oauth/token` refuses a console login's.

## v37.0.0 — an object list shows live objects unless asked for others

- **Breaking, behaviour.** `ListObjects` and `CountObjects` whose filter names
  no `state` leave trashed (`DELETED`) objects out; a collection's objects no
  longer list beside them what its trash lists. Ask for them by naming the
  state: `state == "DELETED"` for the trash, `state in ["AVAILABLE",
  "DELETED"]` for both. Migration 081 replaces `search_object_ids` with a
  signature that takes the choice.
- **`ListCollections` with an empty `parent`** lists every tenant's
  collections for a platform admin; anyone else still gets their own
  tenant's. Its page token now carries the tenant as well as the name, so a
  list across tenants pages without losing rows; a token issued before still
  resumes within its tenant.

## v36.0.0 — every bucket has a tenant owner, and sharing is granted

[ADR-0032](adr/0032-every-bucket-has-a-tenant-owner.md).

- **Breaking, API.** `CreateBucket` requires `owner_tenant_id`: without one it
  is `INVALID_ARGUMENT` with reason `BUCKET_OWNER_REQUIRED`. A pool for many
  tenants belongs to the platform tenant.
- **Breaking, behaviour.** A collection binds only to a bucket its tenant owns
  or holds a grant on; anything else is `PERMISSION_DENIED` with reason
  `BUCKET_NOT_GRANTED` (it used to be accepted for a bucket with no owner, and
  `INTERNAL` for another tenant's bucket). `EnsureTenantStorage` creates its
  bucket owned by the calling tenant and refuses an existing one the tenant
  may not bind to. `SetTenantDefaultBinding` refuses such a bucket too; a
  tenant created with a shared default binding is granted that bucket.
- **New RPCs.** `BucketService.GrantBucket`, `RevokeBucketGrant` (refused with
  `BUCKET_GRANT_IN_USE` while the tenant still has collections in the bucket)
  and `ListBucketGrants`. Moving a bucket to another owner leaves the previous
  owner a grant.
- **Consumers that create a bucket for several of their tenants** through
  `CreateBucket` name its owner and call `GrantBucket` for each tenant they
  provision. One that provisions through `EnsureTenantStorage` needs nothing.
- **Migration 080** gives every ownerless bucket an owner: the one tenant using
  it, or the platform tenant with a grant for each tenant that uses it. The
  platform tenant is found by the bootstrap's default slug, `platform`; a
  deployment that set another `bootstrap.admin.tenant_slug` and still has
  ownerless buckets used by several tenants, or by none, stops the migration
  with a message naming what to do — set their owners first, or rename the
  tenant. `buckets.owner_tenant_id` becomes `NOT NULL`.
- **Stats.** `BucketStats.shared` counts the buckets granted to another
  tenant, and `tenant_owned` the rest.

## v35.0.0, sdk/go/v0.74.0 — a failed object is deleted; FAILED leaves the contract

- **Breaking, API.** `ObjectState.OBJECT_STATE_FAILED` (3) is removed and
  reserved. An object that cannot become available — its upload URL expired
  with nothing at its path, its bytes broke its registration, its copy failed,
  its multipart upload was aborted — is deleted, so a later `GetObject` is
  `NOT_FOUND`, and its key is free for a retry: an upload at that key used to
  be refused with `ALREADY_EXISTS` until someone deleted the failed object. A
  filter naming `state == "FAILED"` is refused as an unknown value.
- **Behaviour.** The reason is logged (`object failed and was discarded`) and
  counted in `paladin_object_failures_total{reason}`.
- **Migration 079** deletes the objects kept as FAILED and drops the label from
  `object_state`, which rewrites `objects` under an exclusive lock. Replicas of
  the previous release still running during the rollout can fail queries on
  `objects` with `cache lookup failed for type` until they are replaced.
- **SDKs.** The fake servers' `MarkFailed` / `mark_failed` are `FailUpload` /
  `fail_upload`, and delete the pending object.

## v34.6.0 — every data-plane write is audited

- **Behaviour, audit.** The data plane records every write in the trail of
  the tenant it acted on: a tenant's own agents and users, not only a platform
  admin acting in it. `DownloadObject`, `PresignDownload` and `PresignPart`
  only issue a URL and are not recorded.
- **Operations.** `audit_log` grows with object traffic, about 1.5 KB per
  object uploaded in one piece; size `housekeeping.audit_log_ttl` for it
  (`backend/docs/ops-housekeeping.md`). Each write pays one more synchronous
  insert, about 1.7 ms on the dev cluster.

## v34.5.2 — capabilities run on limes v0.64.0 and pgstore v0.9.0

- **Behaviour, stats.** The platform stats count capability records through
  pgstore's census instead of its tables. A record below a revoked ancestor
  now counts as revoked rather than active or expired, since the verifier
  refuses it, and the capabilities-expiring drill-down sums to that count.
- **Behaviour, admin.** A write to the capability stores under a platform-wide
  read is refused with `CodeInternal` (pgstore's `ErrScopeMismatch`); before,
  housekeeping writes under it changed nothing and reported success.

## v34.5.0 — billing reads the charges ledger through pgstore

- **Behaviour, billing.** `GetTenantSummary` and `GetTenantTimeSeries` read
  the ledger through pgstore's `Ledger` instead of its table. Amounts are net
  of refunds. The total, the charge count, the top lists and the series are
  all in the unit most of the period's charges are in; a mixed-unit tenant
  used to have every unit summed into one figure. Week buckets start on
  Monday 00:00 UTC, as before.

## v34.4.0 — capabilities run on limes v0.61.0 and pgstore v0.7.0

- **Behaviour, tenants.** `PurgeTenant` erases the tenant through pgstore
  (`EraseTenant`), in the transaction that removes the tenant row: every
  capability of the tenant is revoked, and its counters, holds, charges and
  budget are deleted. A capability that has not been expired for
  `limes.MinPurgeRetention` stays on record, revoked, until the capability
  purger removes it, so its token is refused rather than unknown.
- **Behaviour, admin.** A platform-wide read of the capability stores reads
  only; the capability purger alone writes across tenants. A capability store
  call scoped to another tenant than the one its request names fails with
  `CodeInternal` — a wiring fault in the server — where it used to read as
  `NotFound` or `InvalidArgument`.
- **Deploy.** The migrate step grants the `limes` schema to `paladin_app` and
  `paladin_reaper` after Goose has run, through pgstore. Migration `077` drops
  the `capability_revoked` triggers; revocations now reach every replica on
  pgstore's `limes_revocations` channel. Readiness gains a critical
  `postgres-limes-schema` component, which fails while the `limes` schema is
  behind the one the build queries.

## v34.0.0 — capabilities are stored by limes/pgstore

- **Breaking, data.** Capabilities, their revocations and counters, budget
  holds, the charges ledger and tenant budgets move to the `limes` schema,
  kept by limes/pgstore. Migration `076` drops the old tables and carries
  nothing over: every capability is refused after the upgrade, every tenant
  budget is gone and billing starts empty. Issue capabilities again and set
  tenant budgets again.
- **Breaking, deploy.** The migrate step creates the `limes` schema before
  Goose runs, so the migrate role must be allowed to create a schema in the
  database (`CREATE` on it, as the database owner has).
- **Behaviour, tenants.** `PurgeTenant` deletes the tenant's capabilities,
  budget and charges with it; a tenant that has been charged no longer has
  to keep its row for the ledger.

## v33.3.0 — capabilities run on limes v0.57.0

- **Behaviour, delegation.** `CapabilityService.Delegate` refuses a child
  that would have more than 64 ancestors with `FAILED_PRECONDITION`. The
  store also refuses a child in another tenant than its parent; `Delegate`
  never asks for one, since it narrows the presented capability in its own
  tenant.

## v33.2.0, sdk/go/v0.72.0 — the platform's currency is a setting

A budget or a capability that named no currency used to be in USD: limes'
DefaultUnitCode, which no deployment chose. `capability.currency` (ISO 4217)
now says what an unnamed unit means — a root capability's budget, a tenant's
first budget, an empty billing summary. Unstated it is `XXX`, no currency:
amounts are credits, shown without a symbol. A delegate that names no
currency counts in its parent's.

- The console reads the setting through the new
  `BillingService.GetBillingSettings` and shows it in the budget and
  capability forms. Left alone, those forms send no currency and the server
  applies its own, instead of the `XXX` they used to send.
- A `google.type.Money` that names no currency is no longer USD on the way
  in; its unit is resolved as above.
- Records written before units existed still read as USD: that is what they
  were written in.
- Changing the setting on a deployment with history: a tenant whose budget
  row is in one currency refuses charges from a capability issued in
  another — `FAILED_PRECONDITION`, counted as a unit mismatch in
  `paladin_capability_charges_total`. Set that tenant's budget in the new
  currency, or issue its capabilities in the old one explicitly.

## v33.0.3 — a reissued batch finishes its first run's work

A permanent delete now leaves a tombstone (`erased_objects`, migration 074):
the object's id and collection, written by the statement that removes the
row. A batch permanent delete reissued after its worker died used to report
everything its first run had erased as `not found` — the same answer as an id
that never existed. It now counts those under `already_applied`. Tombstones
are kept as long as terminal operations are
(`worker.jobs.housekeeping.operations_ttl`) and purged by the same job.

A reissued batch copy also finishes what its first run left half done. A copy
cut off between creating its destination row and promoting it left that row
PENDING, and the reissue reported the key as "a write in progress" until the
row's presign deadline passed. Rows a batch copy creates now record the
operation that created them (`objects.created_by_operation`, migration 075);
a reissue adopts a PENDING row whose creator is no longer running and was made
for an object of the same size and type, and copies into it.

## v33.0.2, sdk/go/v0.71.1 — an operation's metadata is its progress

`Operation.metadata` now carries only the operation's progress: a `Struct` of
the latest `{processed, total}` the worker reported, absent until its first
report. Before its first report it used to carry the executor's arguments —
the request as the worker received it — and the runner then overwrote them
with progress, so what a client saw there depended on timing. The arguments
are no longer returned; a client that read them should keep its own request.

Migration 073 gives progress its own column, so the arguments survive the
whole operation, a reclaim after a lost worker included. Rows whose stored
metadata was already a progress snapshot have it moved to the new column.

## v33.0.1 — a reissued batch counts the work already done

A batch operation whose worker died ends FAILED with `WORKER_LOST`, and the
recovery is to issue the same batch again. That second run used to report
everything the first one had applied as a failure: `duplicate key` for a copy,
a conflict for a soft delete, `not found` for a restore. Now the copy,
soft-delete and restore executors count those items as succeeded, and the
response says how many under a new `already_applied` field (included in
`succeeded`, omitted when zero). A copy counts only when the destination holds
the source's etag, size and checksum; any other object at that key is still a
failure, named as such.

## v33.0.0, sdk/go/v0.71.0 — an API token rate limit of 0 is a cap

- **Breaking, API tokens.** `APITokenServiceCreateRequest` and `APIToken`
  carry the rate limit as a `rate_limit` oneof: `rate_limit_rpm`, where 0
  admits no request, or `unlimited_rate`. `Create` refuses a request that
  sets neither with `INVALID_ARGUMENT`. `GetUsage.limit_rpm` is absent for a
  token with no cap.
- **Migration 072.** `api_tokens.rate_limit_rpm` is nullable; every 0 stored
  under the old reading becomes NULL.

## v33.0.0, sdk/go/v0.71.0 — tenants past their trash retention are surfaced

- **API, additive.** The `ListTenants` filter gains `purge_eligible`: trashed,
  with `admin.tenants.trash_retention` lapsed. It is pushed down to SQL.
- **Metrics.** `paladin_tenants_past_trash_retention` counts those tenants,
  sampled by the worker every housekeeping interval.
- **Console.** The dashboard says when there are any, and links to Trash.

## v32.0.0, sdk/go/v0.70.0 — a quota cap of 0 is a cap

- **Breaking, quotas.** `Quota.max_total_bytes`, `max_object_count`,
  `max_bytes_per_day` and `max_objects_per_day` are `optional`: a cap that is
  set is a limit, where 0 allows nothing, and an absent one is no cap.
  `SetQuota` now honours `update_mask`: a named cap takes the request's value,
  a named cap the request leaves absent is lifted, and an unnamed one keeps
  its value. A mask that names nothing, or anything but a cap, is refused with
  `INVALID_ARGUMENT`.
- **MCP.** `paladin_set_quota` reads 0 as a cap of nothing and takes `lift`,
  a list of the caps to remove.
- **Migration 070.** Quota caps are nullable; every 0 stored under the old
  reading becomes NULL.

## v31.0.0, sdk/go/v0.69.0 — a tenant budget of 0 is a cap

- **Breaking, tenant budgets.** `TenantBudget`, `TenantBudgetServiceSetRequest`
  and `GetTenantSummaryResponse` carry the cap as a `budget_ceiling` oneof:
  `max_budget`, where a zero amount allows no spend, or `unlimited_budget`.
  `Set` refuses a request that sets neither with `INVALID_ARGUMENT`; an
  `unlimited_budget` with no `currency_code` keeps the tenant's unit. A
  summary for a tenant with no budget row sets neither.
- **Breaking, capabilities.** `CapabilityUnlimitedBudget` is now
  `UnlimitedBudget`, shared by every budget ceiling. The wire and JSON forms
  are unchanged.

## v29.0.0 — capabilities run on limes v0.53.2

- **Breaking, capability tokens.** Capabilities are JWTs with
  `typ: limes-cap-v4+jwt`; every capability issued before the upgrade is
  refused, Biscuit copies included. Issue new ones.
- **Breaking, delegation.** `CapabilityService.Delegate` narrows only the
  capability the caller presents in `X-Paladin-Capability`, and only one
  issued with the `share` operation; delegating from a capability named by
  id alone is refused with `FAILED_PRECONDITION`. Issue a capability that is
  to be delegated with `share` in its ops.
- **Behaviour, capabilities.** A key-bound capability, and one whose
  `not_before` is still ahead, is issued without a Biscuit form: sealing one
  needs a proof of its holder's key, or a capability already valid.
- **API, additive.** `ErrorReason` gains `CAPABILITY_INVALID_TOKEN`,
  `CAPABILITY_BISCUIT_ATTENUATION`, `CAPABILITY_SOURCE_IP_NOT_ALLOWED`,
  `CAPABILITY_NO_CREDENTIALS`, `CAPABILITY_NONCE_REQUIRED` and
  `CAPABILITY_AUTH_SCHEME_MISMATCH`. The first three replace
  `CAPABILITY_INVALID_SIGNATURE`, `CAPABILITY_INVALID_BISCUIT` and
  `CAPABILITY_SOURCE_NOT_ALLOWED`, which are no longer sent.
- **Breaking, capability ceilings.** `CapabilityCaveats` spells each ceiling
  explicitly: `max_requests` (now `int64`, in the `request_ceiling` oneof) or
  `unlimited_requests`, and `max_budget` (in `budget_ceiling`) or
  `unlimited_budget`, whose `currency_code` is the unit charges are in. 0 is
  a limit that allows nothing. `Issue` refuses caveats that set neither arm
  of a ceiling with `INVALID_ARGUMENT`; `Delegate` keeps the parent's ceiling
  for one left unset. A capability read back carries one arm of each.
  `CapabilityBiscuitCopyUsage.max_requests` and `max_budget` are absent when
  the copy set no limit on that axis, and 0 when it froze it. The store keeps
  "none" as `NULL`; migration 066 rewrites the rows written when 0 meant none.
  A tenant's budget still reads 0 as none.
- **Breaking, configuration.** `capability.charge_per_request_unit` is gone:
  every charge is in its capability's own unit. `capability.verifier_leeway`
  above 5m and `worker.jobs.capability.expired_for` below 6m are refused at
  load.
- **Behaviour, housekeeping.** The capability purger removes expired
  capability records, with their revocations, after `expired_for`; the
  charges ledger keeps naming them. A cost reported for a capability purged
  since is refused.
- **Behaviour, DPoP.** Each capability, rather than each key, holds at most
  1 000 live proof ids.
- **Migrations 066–068.** Explicit ceilings; the meter's tables no longer
  reference `capability_records`, and charges and holds keep their ancestors;
  `dpop_seen_proofs` is recreated, keyed per holder.

## v28.0.0, sdk/go/v0.64.0 — lifecycle transitions are refused

- **Breaking, admin API.** `SetLifecycleRules` refuses a rule whose action is
  a transition with `INVALID_ARGUMENT`. Transitions were stored and never
  applied: no supported backend has storage classes to move objects to. A
  bucket that holds one keeps it, unapplied, until the rules are written
  again without it; the console marks it and offers only its deletion.

## v27.8.0, sdk/go/v0.63.0 — objects deleted from the bucket go to the trash

- **Ingest.** A delete event from the storage backend moves the object to
  the trash once a HEAD where its row places it finds nothing; it used to be
  logged and ignored. The ingest role now HEADs the backends, so its
  NetworkPolicy gains egress to `networkPolicies.storage.namespace`.
- **API, additive.** `RestoreObject` refuses an object whose bytes are gone
  with `FAILED_PRECONDITION` and the new reason `OBJECT_BYTES_MISSING`, and
  answers `UNAVAILABLE` when the backend cannot be reached.

## v27.8.0, sdk/go/v0.63.0 — the Python SDK supports Python 3.15

- **Python SDK.** CPython 3.15 is tested beside 3.13 and 3.14;
  `requires-python` stays `>=3.13`. `biscuit-python` has no 3.15 wheel, so
  the `biscuit` extra builds from source there, as it does on 3.14.

## v27.7.0 — the MCP bridge forwards key-bound capabilities

- **Configuration.** `mcp.dpop_key` / `mcp.dpop_key_secret` give the bridge a
  DPoP key (PEM PKCS#8, Ed25519 or P-256). Every bridge replica must hold the
  same one: give it as `dpop_key_secret` and add the Secret's name to
  `rbac.secretReader.secretNames`. Unset, nothing changes.
- **Agents.** To use a key-bound capability through MCP, delegate it to the
  thumbprint `paladin_get_bridge_key` returns and pass the child to the bridge.

## v27.5.0, sdk/go/v0.62.0 — DPoP server nonces

- **Configuration.** `capability.dpop_nonce` (off by default) makes a
  key-bound capability's proof carry a nonce the server issued (RFC 9449 §8),
  so a captured proof is usable for `lifetime` at most. Every replica must
  share the key: give it as `key_secret` and add the Secret's name to
  `rbac.secretReader.secretNames`; outside `local`, `dev`, `test` and `ci` a
  missing key refuses to start.
- **Clients.** The Go and Python SDKs keep the nonce each response brings and
  answer a `use_dpop_nonce` challenge by resending the call once. Turn nonces
  on only once every client holding a key-bound capability runs an SDK of this
  release: an older one is refused.

## v27.3.0 — DPoP under its own scheme, signed Ed25519

- **Behaviour, capabilities.** A key-bound capability may be sent as
  `Authorization: DPoP <token>`, RFC 9449's scheme; under it, a capability
  that is not key-bound is refused. `X-Paladin-Capability` and
  `Authorization: Capability` work as before.
- **SDKs.** Both sign DPoP proofs `Ed25519`, the name RFC 9864 gives the
  algorithm. The server still reads `EdDSA` from older SDKs.

## v27.2.0 — a capability's unit is any ISO 4217 code

**Behaviour and console.** Tenant budgets and capabilities take any ISO 4217
code; a code ISO 4217 does not define is refused. The console's unit pickers
offer every currency, then `XXX` for metering that is not money.

## v27.0.0 — capabilities run on limes v0.7.0

- **Breaking, capability tokens.** Capabilities are JWTs with
  `typ: limes-cap-v3+jwt` and `alg: Ed25519`; every capability issued before
  the upgrade is refused, Biscuit copies included. Issue new ones. A
  key-bound capability is refused without a valid DPoP proof by the verifier
  itself; the SDKs' proofs keep working.
- **Behaviour, metering.** A counter keeps one unit: a charge or hold in
  another currency, and re-denominating a tenant that still counts spend or
  holds, answer `ErrUnitCodeMismatch`. The same `external_ref` for a different
  amount is `ErrExternalRefConflict`, not a replay. Refunding a charge from a
  tenant period since reset leaves the new period's spend alone. Deleting a
  capability releases its holds. A tenant budget the runtime created is at
  version 0 until a ceiling is set.
- **Behaviour, DPoP.** A proof's method is compared exactly, and each key
  holds at most 1 000 live proof ids: past that, its new proofs are refused as
  replays, and only its.
- **Migrations 057–060.** Capability ceilings move into columns of
  `capability_records`, and the stored caveats and principals are rewritten
  into limes v0.4.0's JSON, which an old row no longer decodes as. A signing
  key read without a configured `kid` is published under its RFC 7638
  thumbprint. DPoP proof ids move to `dpop_seen_proofs`, keyed by the proof
  key's thumbprint.

## v26.1.3 — an API token for the admin plane needs roles

**Behaviour, admin API.** `APITokenService.Create` refuses a token whose
audience names `admin` and that carries no roles, with `INVALID_ARGUMENT`. The
admin plane admits an API token only when it carries roles, so such a token
could never authenticate there. Tokens minted before keep working wherever they
already did; the console no longer offers `admin` as a plane, since it cannot
grant roles.

## sdk/go/v0.58.0 — the Python SDK requires Python 3.13

- **Breaking, Python SDK.** `requires-python` is `>=3.13`; 3.10, 3.11 and
  3.12 are no longer installable or tested. `pyqwest` 0.12, which the SDK now
  requires, dropped 3.10 itself. A worker on an older Python stays on the last
  SDK release that supported it, or moves to 3.13 or 3.14.

## v26.0.0, sdk/go/v0.57.0 — the capability primitive is limes

The capability module moved out of this repository to
[limes](https://github.com/oleg-tkachuk/limes) (`github.com/oleg-tkachuk/limes`,
package `limes`), and its wire format dropped the `paladin_` prefix with it.

- **Breaking, capability tokens.** Every token issued before this release is
  refused after it: the JWT `typ` is `limes-cap+jwt`, the claims are
  `limes_principal`, `limes_caveats`, `limes_parent_id`, `limes_gen` and
  `limes_bsk`, and the Biscuit facts are `limes_op`, `limes_resource_prefix`,
  `limes_resource_uri`, `limes_plane`, `limes_expires`, `limes_bind`,
  `limes_max_requests` and `limes_max_budget_nanos`. The signing key is
  unchanged; issue capabilities again.
- **Breaking, Python SDK.** `attenuate` writes the `limes_*` facts, so an SDK
  of this release narrows only tokens a server of this release issued, and
  the other way round.
- **Go SDK.** Attenuate Biscuits with `limes.Attenuate` from
  `github.com/oleg-tkachuk/limes`; `github.com/oleg-tkachuk/paladin/capability`
  is no longer released (the last tag is `capability/v0.17.1`).
- **Metrics.** The primitive's own instruments are `limes.charge.amount`,
  `limes.charge.decisions`, `limes.charge.current_spend`,
  `limes.request.bumps`, `limes.refund.amount` and
  `limes.reservation.decisions` (`limes_*` in Prometheus). Under their old
  `paladin.capability.*` names the charge histogram normalised to the same
  Prometheus name as Paladin's own `paladin_capability_charge_amount`; it no
  longer does. Paladin's `paladin_capability_charges_total`,
  `paladin_capability_charge_amount` and the `paladin.capability.charged`
  event are unchanged.
- Error text from the primitive starts with `limes:` rather than
  `capability:`; match errors with `errors.Is`, as before.

## v25.5.0 — the compose stack runs Postgres 18

`backend/deploy/docker-compose.yaml` and the e2e stack move from Postgres 16 to
18.6, the version the test suites and the reference cluster run. A major
cannot read the previous major's data directory, and Postgres 18's image keeps
its data under `/var/lib/postgresql/18/docker`, so the `paladin_pg_data`
volume is now mounted at `/var/lib/postgresql`. An existing local volume
starts empty under 18 with the 16 data left beside it; the local stack's data
is disposable, so drop the volume and let the stack recreate it:

```
docker compose -f backend/deploy/docker-compose.yaml down -v
```

A deployment that keeps its data moves majors with `pg_upgrade`, or, on
CloudNativePG, a major version upgrade of the cluster.

## v25.0.0, sdk/go/v0.53.0 — the Go SDK and the server run on connect-go v2

- The Go SDK depends on `connectrpc.com/connect/v2` instead of
  `connectrpc.com/connect`. Generated clients take and return plain messages:
  `client.GetObject(ctx, &datav1.GetObjectRequest{…})`, no
  `connect.NewRequest`, no `.Msg`.
- A generated client is built from `(*paladin.Client).Connect()`:
  `paladinadminv1connect.NewTenantServiceClient(c.Connect())`.
  `HTTPClient()` and `ClientOptions()` are gone.
- `WithClientOptions` is `WithTransportOptions`, taking `connecthttp` options
  (`connecthttp.WithGRPC()`, `connecthttp.WithSendGzip()`). Interceptors of your
  own — otelconnect's, say — go in `WithInterceptors`, as
  `connect.ClientInterceptor`s: `otelconnect.NewClientInterceptor()`.
- Request headers are set, and response headers read, through the call's
  `connect.CallInfo`: `ctx, info := connect.NewClientContext(ctx)`.
- A client reads responses up to `paladin.MaxResponseBytes` (16 MiB), the
  largest the server sends; connect-go v2 alone would stop at 4 MiB.
- On the wire nothing changes: a client on connect-go v1, or any other Connect
  or gRPC client, keeps working against the server.
- A method a plane does not serve, on a service it does, is answered with a
  Connect error, code `unimplemented` (HTTP 501), where it used to get a bare
  `404 page not found`. As before, it is answered ahead of the plane's
  interceptors, so a caller without credentials gets the same answer. An
  unknown service is answered `Unimplemented` as before.

## v24.0.0, sdk/go/v0.52.0 — money is exact: nanos in the core, `google.type.Money` on the wire

Amounts were `float64` in the capability module, so spend drifted — 0.1 then
0.2 was refused under a ceiling of 0.3 — and the API carried micros, so a
cost below a millionth rounded away. Every amount is now counted exactly, to
the nano (a billionth of the unit), and nothing on the client's side needs
summing apart or correcting.

- **Breaking, capability module.** Every amount is `capability.Nanos`
  (`int64` billionths) instead of `float64`: request, receipt, usage, budget
  and caveat fields alike, and `Meter.Refund` returns `Nanos`. Write one with
  `ParseAmount`/`MustParseAmount("0.35")`, or `AmountFromFloat` for a price
  that arrives as a float; `MicrosPerUnit`, `MaxMicros`, `AmountToMicros` and
  `MicrosToAmount` are gone. `CopyCeiling.MaxBudgetMicros` and
  `Attenuation.MaxBudgetMicros` are `MaxBudget Nanos`. An untyped integer
  constant still compiles but now means nanos: `Amount: 5` is five
  billionths, so convert literals with `MustParseAmount`. A token's budget is
  still a JSON number of units, so issued tokens verify unchanged.
- **Breaking, unit codes.** Unit codes are ISO 4217: USD, EUR, UAH, GBP, and
  XXX — ISO 4217's code for "no currency" — for a budget that is not money.
  `UNIT`, the name XXX replaces, is refused everywhere: in tokens, records,
  requests and `capability.charge_per_request_unit`. Reissue a capability
  that carries it.
- **Breaking, Biscuit.** A copy's budget is the attenuation fact
  `paladin_max_budget_nanos`; `paladin_max_budget_micros` is no longer part
  of the vocabulary, and a copy carrying it is refused. The Python SDK's
  `attenuate` takes `max_budget_nanos`. Attenuate such a copy again.
- **Breaking, API.** Every `*_micros` field and the `unit_code` beside it are
  replaced by a `google.type.Money` that carries both, exact to the nano:
  `CapabilityCaveats.max_budget`, `CapabilityServiceGetUsageResponse.spent`,
  `CapabilityBiscuitCopyUsage.max_budget`/`spent`/`reserved`,
  `TenantBudget.max_budget`/`spent`, `TenantBudgetServiceSetRequest.max_budget`,
  `GetTenantSummaryResponse.total`/`max_budget`, and `spent` on `TopEntry`
  and `TimeBucket`. The old numbers and names are reserved, and a binary
  request that still sends one is refused rather than read as unlimited. A
  zero `max_budget` is no budget in its currency; an absent one is no budget
  in USD, and on `TenantBudgetService.Set` lifts the cap and keeps the unit.
- **Breaking, events.** `paladin.capability.charged` carries `amount`, in the
  CloudEvents envelope's `data`, as a Money object —
  `{"currency_code": "USD", "units": "1", "nanos": 500000000}`,
  `units` a string as in the proto JSON mapping — in place of the float
  `amount`, `amount_micros` and `unit_code`. A subscription filter on the
  old keys matches nothing.
- **Database.** Migrations 055 and 056 widen every money column to nine
  decimals — a catalog change, not a rewrite — bounded at
  `capability.MaxNanos` by a CHECK, and rename
  `capability_reservations.copy_max_budget_micros` to `copy_max_budget_nanos`
  with its values in nanos.
- **Configuration.** `capability.charge_per_request_amount` is read exactly,
  to nine decimals, and refuses a negative value.

## v23.0.1 — a Biscuit copy with limits cannot delegate

**Behaviour, capability module.** `Issuer.Delegate` refuses a `Parent` that
carries `Copies` — a Biscuit copy with limits of its own — with
`ErrDelegationTooWide`. A child counts against the capability, never the copy,
so delegating let a copy shed its limits; a consumer that did not refuse it
itself was open to that. Paladin's delegation endpoint already refused it.

**Behaviour, capability module.** `NewRemoteJWKSResolver` refuses intervals
that do not nest, `MinRefreshInterval ≤ RefreshInterval ≤ MaxStale`, after
applying the defaults. A `MaxStale` below `RefreshInterval` made the resolver
fail closed with no fetch having failed.

## v23.0.0, sdk/go/v0.51.0 — the capability module's API, made uniform

**Breaking, capability module.** Every input is a `…Request` and every read
names what it reads. Renamed, with no change in behaviour:

| Before | After |
|---|---|
| `RevokeArgs`, `RevokeBiscuitArgs` | `RevokeRequest`, `RevokeBiscuitRequest` |
| `ListByPrincipalArgs` (field `PrincipalT`) | `ListByPrincipalRequest` (field `PrincipalType`) |
| `SetTenantBudgetArgs`, `ListTenantBudgetsArgs` | `SetTenantBudgetRequest`, `ListTenantBudgetsRequest` |
| `Meter.BumpRequest(RequestBump)` | `Meter.Bump(BumpRequest)` |
| `Meter.Get` | `Meter.GetUsage` |
| `ValidateOverrun(p)` | `p.Validate()` |

The metric names are unchanged.

A `Capability` now crosses the API by value: `Store.Get`, `Verifier.Verify`,
`Issuer.Issue`, `Issuer.Delegate` and `Decode` return one, and
`Issuer.Biscuit` and `DPoPVerifier.Check` take one, where each used a pointer.

`CacheOption` now configures both revocation caches, so it is built only by
`WithCacheClock` and `WithMaxEntries`; an option written against
`*CachedRevocationChecker` no longer compiles. `CachedBiscuitRevocationChecker`
gains `Sweep`. `WithMetering` returns a store that implements
`CopyUsageReader` exactly when the store it wraps does — assert the interface
on its result rather than reaching past it.

What the stores write can now be read back. `Store.GetRecord` returns a
capability with the principal that issued it and its own revocation entry;
`BiscuitRevocationStore.GetBiscuitRevocation` returns a revoked copy's entry;
`Meter.GetCharge` reads a charge by the id `Refund` takes. All three are new
interface methods, so a store of your own must add them. So are
`Meter.GetReservation` and `Meter.ListReservations`, and `Reserve` now returns
the whole hold, which both read back the same way. Migration `054` adds
`capability_records.issued_by`; a capability recorded before it reads back
with the issuer's subject alone. `memstore.Store.IssuedBy` is gone — use
`GetRecord`.

`ListTenantBudgets` pages like `ListByPrincipal`: it takes a `Cursor` and
returns the next one, and `TenantBudgetService.Summarize` carries them as
`page_token` / `next_page_token`. Its order is now utilisation, unclamped,
then tenant id — the tenant furthest past its ceiling leads, and ties no
longer fall to the slug.

`CapabilityService.Get` returns one capability as it is on record — who asked
for it and its own revocation, if any — and the console's capability details
show both. `CapabilityServiceRevokeResponse` no longer promises a count of the
descendants a cascade revoked.

`capability/storetest` checks a `Store` against the contract, which now says
what it left open: an id already on record is `ErrAlreadyExists`, revoking an
unknown id is `ErrNotFound`, `PurgeExpired` drops revocation entries and never
records, and `ListByPrincipal` takes the whole principal and pages in id order
(`PageLimit`). `memstore` follows it: it used to accept a duplicate, revoke an
unknown id, delete expired records, list without paging, and cascade a
revocation down from capabilities revoked earlier as well as the one named.

## v22.0.0, sdk/go/v0.50.0 — capability refusals carry a reason; costs reported later

- A refused capability now carries a `google.rpc.ErrorInfo` in the `paladin`
  domain, with a reason from the new `ERROR_REASON_CAPABILITY_*` values:
  expired, revoked, op not allowed, budget exceeded and the rest. The Connect
  codes are unchanged. The comments on `ErrorReason` say which refusals are
  permanent — the same request with the same capability will never pass — so
  a client can stop instead of retrying.
- The capability module: `CheckRequest.Effect` declares what a consumer-defined
  operation does to state; `ReasonOf` and `Reason.Permanent` name a refusal;
  `ChargeRequest.ExternalRef` makes a charge idempotent, and `Overrun:
  OverrunRecord` records a cost already incurred past a ceiling.
- **Breaking, capability module:** settling a reservation that is already
  settled returns that charge with `Replayed` set, where it returned
  `ErrReservationNotFound`. A released or expired reservation still returns it.
- **Breaking, capability module:** `Meter` gains `ChargeByRef`. A `Meter` of
  your own must also honour `ChargeRequest.ExternalRef` and `Overrun`, and
  `SettleRequest.Overrun`; run `capability/metertest` from its tests to check
  that it does.
- Migrations `051`–`053` add `external_ref`, `reservation_id` and `overrun` to
  `charges`, with two unique partial indexes built concurrently.

## sdk/go/v0.47.0 — the Python SDK runs on connectrpc

- The Python SDK depends on `connectrpc` 0.12 (connect-python's successor)
  instead of `connect-python` 0.9. Messages stay `google.protobuf`: the stubs
  are generated with the plugin's `protobuf=google` option.
- An interceptor of your own reads `RequestContext` and `ResponseMetadata`
  fields as properties: `ctx.request_headers`, `ctx.method`, `ctx.timeout_ms`,
  `meta.headers` — no call.
- `ConnectError.details` are connectrpc `ErrorDetail`s rather than
  `google.protobuf.Any`. Read one with `paladin.unpack_detail(detail,
  error_details_pb2.ErrorInfo)`; attach one with `paladin.error_detail(msg)`.
- `proto_json=True` is gone from the client options: pass
  `codec=connectrpc.compat.google_protobuf_json_codec()`. `protocol`,
  `read_max_bytes` and the compression options reach every generated client
  through `transport` as before.
- `connectrpc_otel.OpenTelemetryInterceptor(client=True)` now works in
  `interceptors`, and traces each RPC.

## v21.0.0, sdk/go/v0.48.0 — Paladin registers only the buckets it means to manage

- `CreateBucket` with `provision_on_backend` refuses a bucket the backend
  already holds (`ALREADY_EXISTS`, `BUCKET_EXISTS_ON_BACKEND`); register an
  existing bucket with `provision_on_backend` false, which in turn refuses one
  the backend does not hold (`BUCKET_NOT_ON_BACKEND`); a bucket registered
  that way is never public. The feature probe's scratch buckets cannot be
  registered (`BUCKET_RESERVED`).
- `EnsureTenantStorage` refuses a bucket that exists on the backend but is not
  registered: have an operator register it first. Registered buckets are
  unaffected.
- `Bucket.created_on_backend` says whether Paladin created the bucket. A
  delete with `delete_on_backend` is refused for any other
  (`BUCKET_NOT_CREATED_BY_PALADIN`); buckets registered before this release
  count as not created by Paladin.
- A CreateBucket on a name another S3 account owns now fails instead of
  being taken as success.

## v20.1.0 — background jobs pass a trashed tenant by

- Lifecycle expiry, replication, the object-trash hard-deleter, the pending
  upload reconciler and storage-event promotion, storage-layout migrations,
  batch operations and event delivery skip a tenant in the trash, and resume
  on restore. Its queued operations stay `PENDING` and its events stay
  pending meanwhile; a purge removes both with the tenant.

## v20.1.0, sdk/go/v0.45.0 — a delete helper in both SDKs

- `paladin.Delete` (Go) and `paladin.delete` / `adelete` (Python) supply the
  `resource_version` every `DeleteObject` requires: they read the object,
  delete it at that version, read it again if it changed meanwhile (up to
  `DeleteAttempts` / `DELETE_ATTEMPTS`), and treat an object already gone as
  done. A consumer that called `DeleteObject` without a version — refused by
  the server and by both fakes — can switch to them.

## v20.0.0 — a tenant in the trash is frozen

- Every change to a tenant in the trash is refused — `FAILED_PRECONDITION`,
  reason `TENANT_ALREADY_DELETED` — on the data, admin and IAM planes,
  platform admins included: uploads, deletes, copies, tags, collections,
  buckets, quotas, budgets, policies, subscriptions, users, new credentials
  and `UpdateTenant`. (Any other principal was already refused by the tenant
  gate, its own tenant being the trashed one.) Reads, downloads, revocations,
  cancellations and `Get`/`List`/`Restore`/`PurgeTenant` stay open. A tenant
  holding data is removed by restoring, emptying, trashing and purging it.
- A slug a trashed tenant held and a live tenant has since taken names the
  live tenant everywhere; before, which one it resolved to was undefined.

## v20.0.0, sdk/go/v0.44.0 — public collections

- A bucket can be created **public** (`Bucket.public_read`, with
  `provision_on_backend`) and a collection **public**
  (`Collection.access = PUBLIC_READ`): anyone may read its objects, unsigned,
  at `Object.public_url` ([ADR-0027](adr/0027-public-collections.md)). Both
  are fixed at creation, need the new Cedar action `ConfigurePublicRead`
  (built-in for `platform.admin` and `platform.tenant-provisioner`), and a
  public bucket is refused (`BACKEND_FEATURE_UNSUPPORTED`) unless the
  backend's probe found `ANONYMOUS_READ_POLICY` supported — run `TestBackend`
  first.
- In a public collection the server names objects: an upload, multipart
  initiate or copy that supplies a key is refused (`PUBLIC_COLLECTION_RULE`),
  as is a delete without `permanent=true`. `CopyObjectRequest.destination_key`
  is now optional; empty keeps the source's key, as the handler always did.
- A backend's `public_endpoint` is part of every public URL a consumer stored
  while it hosts a public bucket without `public_base_url`; changing it breaks
  them.
- New error reasons: `BACKEND_FEATURE_UNSUPPORTED` (26),
  `PUBLIC_COLLECTION_RULE` (27). Migration 049.
- Both SDK fakes serve public collections (`PublicCollection`,
  `public_collection`).

## v20.0.0, sdk/go/v0.44.0 — a storage backend's S3 features are probed

- `TestBackend` now also probes, on a reachable backend, each S3 feature
  Paladin uses — conditional PUT, SHA-256 checksums, multipart upload,
  server-side copy, form upload, bucket creation, an anonymous-read bucket
  policy — and records what it found (migration 048). `StorageBackend` gains
  `features` and `compatibility`; the console shows both, with a warning for
  each unsupported feature.
- The probe creates and deletes a scratch bucket named `paladin-probe-…`, and
  sets and deletes a policy on it. Credentials that may not do so get those
  features reported `UNKNOWN`; the object checks then run under
  `.paladin-probe/` in the backend's configured bucket.
- `TestBackend` takes longer: up to 20 seconds more on a slow store, inside
  the admin listener's default 30-second write timeout. A deployment that
  lowered `admin.server.write_timeout` below 25 seconds should raise it.

## v20.0.0 — a trashed tenant's credentials stop working

- **Every plane refuses a credential whose tenant is in the trash** —
  `FAILED_PRECONDITION` with reason `TENANT_ALREADY_DELETED` — and one whose
  tenant no longer exists, `UNAUTHENTICATED`. Moving a tenant to the trash
  revokes nothing: restoring it makes its API tokens, capabilities and
  sessions work again. A change reaches every replica at once through the
  `tenant_state` notification (migration 047); a state that cannot be read
  refuses the call as `UNAVAILABLE`.
- Calls with no principal (`Login`, `RefreshToken`), and principals holding
  a platform role (`platform.admin`, `platform.capability-issuer`,
  `platform.tenant-provisioner`), are not affected: their authority is not
  their tenant's, and a trashed platform tenant must not lock out the admins
  who could restore it.

## v19.0.0, sdk/go/v0.44.0 — an internal error no longer carries its cause

- **An RPC that fails on the server's side answers `internal error; request
  id <id>`** with its code (`internal`, `unknown`, `data_loss`), its details
  and its metadata, instead of the error's own text — which carried driver
  messages, table and constraint names. The log line and the trace keep the
  original under the same request id, returned in `X-Request-Id`; a request
  sent without one is given one. Code that parsed an internal error's message
  has nothing to parse.

## v18.0.0, sdk/go/v0.43.0 — capability issuance answers each failure by its kind

- **`CapabilityService/Issue` and `Delegate` no longer answer every failure
  `INVALID_ARGUMENT`.** A malformed request still does. A subject tenant that
  does not exist answers `NOT_FOUND` with the new reason `TENANT_NOT_FOUND`
  — a tenant being provisioned may exist shortly, so a caller can retry on
  that reason — and one in the trash `FAILED_PRECONDITION` with
  `TENANT_ALREADY_DELETED`; before, issuing for a trashed tenant succeeded.
  A store or signer failure answers `INTERNAL`. Code that matched
  `INVALID_ARGUMENT` to mean "tenant not ready" should match the reason.
- `Delegate`'s admin path answers `NOT_FOUND` only for a parent that is not
  there; a store failure reading it is `INTERNAL`.
- The `capability` module's request errors match `ErrInvalidRequest`, and
  their messages now read `capability: invalid request: …`.

## sdk/go/v0.42.0 — a multipart download's checksum is verified

- **A multipart object completed from now on records a composite checksum**:
  the digest of its parts' digests, in S3's COMPOSITE form
  (`<base64>-<parts>`), and `ChecksumDigest.part_size_bytes` beside it. Both
  SDKs recompute it while a whole download streams and fail the read with
  `IntegrityError` on a mismatch, as they already did for an object uploaded
  in one piece.
- A multipart object completed before this release has no checksum recorded
  and is still checked for its size alone; there is nothing to backfill it
  from, since the parts' checksums were never kept.
- An older SDK ignores the new field; a new SDK against an older server sees
  no part size and does not check a composite.

## v17.0.0, sdk/go/v0.41.0 — the SDK fakes refuse what the server refuses

- **`paladintest` (Go) validates every request** with the contract's
  protovalidate rules, as the server does: a test that uploads with no
  content type, or deletes with no `resource_version`, now fails with
  `ErrInvalidArgument` where it used to pass.
- **`DeleteObject` checks `resource_version`**, and every change to an object
  advances it: a stale version is `ErrVersionConflict`. A test that soft
  deletes and then purges must use the version after the soft delete.
- **`ListObjects` refuses `filter`, `order_by` and `sort_order`** as
  `Unimplemented`; it used to list as if they were not set.
- **Python: `paladin.testing.FakePaladin` needs the `testing` extra**
  (`paladin-sdk[testing]`), for protovalidate, and holds requests to the same
  rules — `InvalidArgumentError`, `VersionConflictError`, `UNIMPLEMENTED`.

## v16.0.0, sdk/go/v0.40.0 — the Python SDK's `TLS` needs the `tls` extra

- **`paladin.TLS(...)` raises `ImportError` unless the SDK is installed with
  the `tls` extra**: `paladin-sdk[tls] @ git+…`. `httpcore`, `h2`, `anyio` and
  `cryptography` are no longer installed for everyone; plaintext clients and
  pyqwest's own TLS need none of them.
- **`cryptography` moved to the `tls` and `dpop` extras.** A client given
  `dpop_key=` needs `paladin-sdk[dpop]`, which it was already documented to.

## v15.2.0, sdk/go/v0.39.0 — `CompleteMultipartUpload` returns the stored object

- **`CompleteMultipartUpload` answers with the whole object** — collection,
  key, size, checksum, timestamps — where it carried the name alone. A client
  that read the object back with `GetObject` after completing can stop; the
  SDKs' `CompleteMultipart` / `complete_multipart` still do it against a
  server that answers with the name only.
- New in both SDKs: the control half of a browser-sent multipart upload
  (`BeginMultipart`, `PresignPart`, `CompleteMultipart`, `AbortMultipart`),
  `Ensure`, and a per-key `CapabilityCache`; Python gains `capability_source`.

## v15.1.0, sdk/go/v0.38.0 — SDK idempotency keys, errors and names

- **A context or block idempotency key no longer goes on calls the contract
  declares side-effect free or idempotent**, and the helpers give their
  repeated calls (DownloadObject on a retry, PresignPart per part) keys of
  their own; `UploadMany` / `upload_many` narrow the key to `key/<n>` per
  input. Code that relied on one key reaching every call made with it now
  sees it only on the mutating ones.
- **Python raises `PaladinError` for every failed call**: a code with no kind
  of its own (`UNAVAILABLE`, `INTERNAL`) was a bare `ConnectError`. An
  `isinstance(err, PaladinError)` used to tell the two apart is now always
  true; `InvalidArgumentError` (Go: `ErrInvalidArgument`) is new.
- **Python name constructors check their parts** as `.parse` does:
  `CollectionName("acme", "docs")` raises `InvalidNameError` (the tenant must
  be its id) instead of building a name the server refuses.
- Sessions take client options (`WithSessionClientOptions`; Python
  `transport=`, `tls=`, `**client_options`), serve a cached token while
  another is minted, and the Go SDK reports an IAM outage as `Unavailable`
  rather than `Unauthenticated`.

## v15.0.1, sdk/go/v0.38.0 — a reused `Idempotency-Key` with a different request is refused

- **The same `Idempotency-Key` sent with a different request to the same
  method answers `InvalidArgument`** instead of replaying the first request's
  response. Before, a client reusing one key — the SDKs' context key does,
  inside their download and multipart helpers — got one object's download URL
  under every name. Use one key per request; a retry of the same request
  still replays.

## v15.0.0 — `housekeeping.pending_ttl` and `delete_orphaned_parts` are removed

- **A config that sets `worker.jobs.housekeeping.pending_ttl` or
  `worker.jobs.housekeeping.delete_orphaned_parts` no longer loads.** Neither
  was read. A PENDING object expires with its presigned URL
  (`limits.presign.put_ttl`), and an abandoned multipart upload's parts are
  freed when the multipart reaper aborts it after `multipart_ttl` — whatever
  `delete_orphaned_parts` said. Delete both keys before upgrading.

## v15.0.0 — `storage.backends.<name>.auth.mode` has no default

- **A storage backend that names no `auth.mode` no longer loads.** The schema
  used to fill `default_chain`, so a backend configured without credentials
  quietly used whatever the AWS chain found — environment, the node's role,
  IRSA. Name the mode on every backend; the chart's `primary` already does
  (`static_keys`).

## v14.0.0 — `security.reject_tenant_mismatch` is removed

- **A config that sets `security.reject_tenant_mismatch` no longer loads.**
  The key was read by nothing: the REST middleware it once switched went with
  the move to Connect, and since then a request naming another tenant has
  been refused (or, for a platform admin, acted on and audited) whatever the
  key said. The strict loader rejects it now, so delete it from any values
  file or overlay before upgrading. Nothing about tenant checks changes.

## v11.2.0, sdk/go/v0.31.0 — a multipart upload to a taken key is `AlreadyExists`

- **`InitiateMultipartUpload` at a key another object holds answers
  `AlreadyExists`**, as `UploadObject` always has. It answered `Internal`, so
  a client could not tell a conflict it can act on from a server failure. A
  key is held by an object in any state, the trash included, until a
  permanent delete.
- **`paladintest` and `paladin.testing` hold one object per key as well**, and
  keep an upload's metadata and tags; `LookupObject` finds an object in any
  state but deleted, and `MarkFailed` / `mark_failed` fail a pending one. A
  test that uploaded twice to one key against the fake now meets
  `ErrAlreadyExists` / `AlreadyExistsError`, as it would against the server.

## v11.1.1 — the data plane acts on the tenant a platform admin names

- **A platform admin's data-plane calls on another tenant now reach that
  tenant.** They used to act on the admin's own tenant — empty lists, and
  writes into a same-named collection there. They are now evaluated against
  the target tenant's policies and quotas.
- **Names in one request must agree on the tenant.** A copy or batch whose
  names span two tenants is `PermissionDenied`, and so is a version name or a
  multipart completion naming a tenant other than the caller's, which used to
  be served the caller's own tenant silently.
## v11.0.0 — webhook deliveries no longer carry `X-Paladin-Signature`

- **The body-only `X-Paladin-Signature` is gone**, after one release
  (v10.5.0) beside `X-Paladin-Webhook-Signature`. A subscriber that still
  verifies it refuses every delivery: verify `X-Paladin-Webhook-Signature`
  with `paladin.VerifyWebhook` or `paladin.verify_webhook` instead.

## v10.5.0 — webhook deliveries carry a timestamped signature

- **An HTTP subscription with a signing secret now also receives
  `X-Paladin-Webhook-Signature: t=<unix seconds>,v1=<hex>`**, an HMAC-SHA256 of
  `<t>.<body>` under the same secret. Verify it with `paladin.VerifyWebhook`
  (Go) or `paladin.verify_webhook` (Python), which refuse a delivery more
  than five minutes from the subscriber's clock — a captured delivery can no
  longer be replayed later. Deduplicate on `X-Paladin-Event-Id` against a
  replay inside the window.
- **`X-Paladin-Signature` is deprecated.** It signs the body alone and so
  verifies forever once captured. It is still sent beside the new header for
  this release, and removed in the next: move verification before upgrading
  past it.

## v10.4.1, sdk/go/v0.29.0 — `BatchUpdateTags` is capped like the other batch RPCs

- **The handler refuses more than 10 000 object ids with
  `InvalidArgument`,** as `BatchDelete`, `BatchCopy` and
  `BatchRestoreObjects` already did. The tag handler never had the check,
  and a test had pinned its absence as intentional with no reason given.
- **RPC clients see no change.** `ObjectSelector.names` is already capped at
  100 by the proto validation; the cap reaches only code that calls the
  handler directly.

## v10.0.0, sdk/go/v0.26.0 — money is micros only; the deprecated doubles are gone

The double money fields deprecated beside `*_micros` are removed and their
numbers and names reserved: `CapabilityCaveats.max_budget_amount`,
`CapabilityServiceGetUsageResponse.spent_amount`,
`TenantBudget.max_budget_amount` and `spent_amount`,
`TenantBudgetServiceSetRequest.max_budget_amount`,
`GetTenantSummaryResponse.total_amount` and `max_budget_amount`, and `amount`
on `TopEntry` and `TimeBucket`. Read and send the `*_micros` field instead:
millionths of `unit_code`, so 25 USD is `25000000`.

- **A request that still sends a removed budget is refused** with
  `InvalidArgument`, over the binary protocol — both SDKs' default. Read as
  absent, it would have issued the capability with no budget, or lifted the
  tenant's cap to unlimited.
- **Over Connect JSON the server cannot tell.** Its JSON codec drops unknown
  names, so `"maxBudgetAmount": 25` from a JSON client is ignored and the
  budget is unlimited. Move JSON callers to `maxBudgetMicros` before
  upgrading the server.
- `max_budget_micros` stays `optional`; absent means 0, no budget.


- **`checksum_value` is required** on `UploadObjectRequest` and
  `PresignPartRequest`, and on every `CompletedPart`: base64 of the body's
  digest under the upload's `checksum_algorithm`, as S3 writes it.
  `size_hint_bytes` is now the exact size (0 is an empty object). Both are
  signed into the URL — PUT and part URLs carry `Content-Length`, the
  checksum and, for a PUT, `If-None-Match: *` — and the object store refuses
  any other body. Send every header in `required_headers`. The Go and Python
  SDKs and the console do this for you; a client that presigns itself uses
  `paladin.Checksum` / `paladin.checksum`.
- **A PUT cannot overwrite.** A second PUT through an upload URL answers 412;
  treat it as already stored and complete the object.
- **POST is a real POST policy** binding the size, Content-Type and checksum;
  submit every field in `post_policy.fields`. It cannot refuse an overwrite.
- **Promotion checks the stored bytes** against the registered size and
  checksum; bytes that differ are deleted and the object fails with
  `FailedPrecondition`.
- **`RegenerateUploadUrl` refuses objects registered before this release**
  (no size or checksum to bind): start a new upload instead.
- **Downloads may be bound to the object's ETag** with `require_etag_match`;
  the SDKs always bind theirs. A browser navigation cannot send `If-Match`,
  so the console does not.
- **MCP:** `paladin_upload_object` takes `size_bytes` (was `size_hint_bytes`)
  and a required `checksum_value`; `paladin_presign_part` and each completed
  part need `checksum_value`; `paladin_initiate_multipart_upload` now sends a
  checksum algorithm (it was refused without one).
- **Deploy order:** browser uploads send `x-amz-checksum-*`, `If-None-Match`
  and `If-Match`; the storage endpoint's CORS must allow them and expose
  `ETag` before the console is upgraded.

## v10.5.0 — a Biscuit copy's usage can be read

- **Go: `BiscuitCopy` names the copy's limits** (`Limits`), and names a copy
  carrying limits on a verifier without `MeterCopies` too, where it used to
  refuse it — so such a copy can be read and revoked anywhere.
- **`CopyUsageReader` reads a copy's counters**; implement it beside your
  `Meter` to show them. `CapabilityService.GetBiscuitUsage` and the console's
  "Copy usage" serve them.

## v10.4.0, sdk/go/v0.28.0 — a Biscuit copy can carry request and budget limits of its own

- **Go: a `Meter` of your own should count `Copies`.** `RequestBump`,
  `ChargeRequest` and `ReserveRequest` carry the presented copy's limits;
  check and count each under its revocation id, record the ids on the charge
  and the reservation, and return to them on refund, settle and release.
  `memstore` shows how. Then set `VerifierConfig.MeterCopies`; until you do, a
  copy carrying limits is refused with `ErrCopyCountersNotMetered`.
- **Pass `cap.Copies` on** wherever you build those requests from a verified
  capability.
- **Python: `attenuate` takes `max_requests` and `max_budget_micros`.**
- **`Delegate` from a Biscuit copy narrows from the copy.** A child used to
  be narrowed from the capability's stored record, so it could regain what the
  copy had given up offline. A copy with limits of its own cannot delegate at
  all (`FailedPrecondition`): attenuate it instead.

## v10.1.0 — one copy of a Biscuit can be revoked on its own

- **Go: a verifier with `AcceptBiscuit` needs `BiscuitRevocations`.**
  `NewStandardVerifier` refuses the config without it. Implement
  `BiscuitRevocationStore` beside your `Store` (`memstore.Store` does) and
  pass it wrapped in `NewCachedBiscuitRevocationChecker`, cleared with the
  other cache when a revocation is announced.
- **Revoking a copy reaches only what was attenuated from it.** The copy it
  came from, its siblings and the capability's JWT keep working; revoke the
  capability to stop them all.

## capability/v0.10.0 — the DPoP replay cache is shared across replicas

- **Go: `ReplayCache.Seen` and `DPoPVerifier.Check` take a
  `context.Context` first.** Pass the request's context to `Check`; a
  `ReplayCache` of your own adds the parameter to `Seen` and should honour it.
  `MemoryReplayCache` ignores it.
- **A `ReplayCache` that cannot answer should report the id as seen.** The
  proof is then refused rather than accepted unrecorded.
- **Paladin keeps proof ids in Postgres** (`dpop_seen_jti`, migration 037)
  instead of per process, so a proof replayed against another replica is
  refused. Every request with a key-bound capability costs one write; the
  capability purger deletes expired ids on its existing interval.

## v10.3.0 — batches act only on their own collection, and take restricted capabilities

- **A batch acts only on objects of the collection it names.** An object id
  from another collection of the tenant is reported as not found, like an id
  that does not exist; before, the worker acted on it although the batch was
  authorised for its own collection only.
- **A resource-restricted capability can run `BatchDelete`,
  `BatchUpdateTags`, `BatchRestoreObjects` and `BatchCopy`.** Each object the
  batch acts on must be in scope, or the batch is refused with
  `PermissionDenied` — as is one that would act on none of them, since
  nothing in it is shown to be in scope.
- **`BatchCopy` needs `get` as well as `put`** from a capability — `get` on each
  source, `put` on each destination — and a capability without
  `AllowTaintedRead` cannot copy a tainted object.
- Calls made with a JWT or API token and no capability are unaffected.

## v7.1.0, sdk/go/v0.24.0 — capabilities need the op for operations and storage bootstrap

- **`OperationService` asserts a capability op.** `GetOperation` needs `get`,
  `ListOperations` `list`, and `CancelOperation` `manage`. A capability
  restricted by resource prefix or URI is refused all three, as it already was
  for the batch RPCs that create operations.
- **`EnsureTenantStorage` needs `manage`.** A capability alone authenticates a
  data-plane call and the Cedar permit is tenant equality, so until now any
  capability of the tenant could provision its buckets and collections.
- Calls made with a JWT or API token and no capability are unaffected.


## v7.0.0, sdk/go/v0.24.0 — uploads are held to `limits.*` and bucket constraints

- **`limits.*` is enforced.** `max_object_size` now refuses a larger single
  PUT/POST, `max_multipart_size` a larger multipart upload, and
  `allowed_content_types` any other media type, all with `InvalidArgument`.
  These were documented as enforced and were not; a deployment that relied on
  their being ignored — the chart ships an allowlist — must widen them first.
  A bucket's constraints (`max_object_size_bytes`, part limits,
  `allowed_content_types`, `required_checksum_algorithm`, presign TTL
  ceilings) now apply to uploads, copies and URLs for that bucket, narrowing
  the global limits.
- **Config keys removed.** `limits.presign.default_max_size` (use
  `limits.max_object_size`) and, since #215, `limits.presign.default_ttl` (the
  per-method `put_ttl`/`get_ttl`/`part_ttl` are the defaults). The loader
  rejects unknown keys, so delete them from any override.
- **Part sizes are IEC.** `min_part_size`/`max_part_size` default to `5MiB`/
  `5GiB`; a `min_part_size` below S3's 5 MiB, including the old `5MB`, fails
  at load.
- **Presign TTLs are refused, not shortened.** A TTL above `max_ttl` (or a
  bucket ceiling) is `InvalidArgument` on every presigning RPC, and
  `max_ttl` may not exceed 168h.
- **Bucket creation validates constraints.** Constraints no upload could
  satisfy are refused by CreateBucket.

## sdk/go/v0.23.1 — the Python SDK knows its version under Poetry

- **`sdk_version()`, the `User-Agent` and errors report the release** when
  the package is installed from its git tag by a tool that builds without
  git, as Poetry does through Dulwich. Such a build still records
  `0.0.0+unknown` in its metadata; the SDK now reads the tag the installer
  recorded in `direct_url.json` instead. Nothing to change on the caller's
  side.

## v0.23.0 — TLS that a consumer can wrap, verify and close

- **Go: `TLS.RoundTripper()` returns the rotating transport** `WithTLS` and
  `WithTransferTLS` use, as a `*RotatingTransport`, for a client of your own
  that wraps it — give it to `WithHTTPClient` or `WithTransferHTTPClient`.
  After a rotation every new request, HTTP/2 included, goes on a connection
  with the new files; before, a busy HTTP/2 connection kept taking requests
  on the old certificate. `TLS.Transport()` is unchanged.
- **Python: `TLS(server_id=…, verify_peer=…, min_version=…)`**, as in Go,
  with the same errors by name (`ServerIDError` and the rest, each a
  `ValueError`). The connections under `TLS` now run on the standard
  library's `ssl` under httpcore, which the SDK depends on: pyqwest cannot
  check a peer. After a rotation no request goes on an old connection, and the
  old ones close once idle. A pyqwest-only setting passed to
  `TLS.sync_transport()` still works, with a `DeprecationWarning`, for one
  release.
- **Python: the exit abort reported against 0.17 did not reproduce.** 200
  runs each on macOS — 0.17.0, 0.17.0 with grpcio, and this release over
  mutual TLS — exited cleanly, so nothing changed for it. A stress test now
  makes and drops clients and transfers in fresh interpreters, 200 times per
  CI run, and fails on any abort.

## v6.7.0, sdk/go/v0.20.0 — the SDKs answer a consumer's review

The contract does not change. What a caller may notice:

- **Python: a generated client takes `http_client=client.http_client()`.**
  The SDK reads the server's release and `Retry-After` at the transport now,
  not from connect-python's internals. `connect()` builds such a client
  itself; one built without it raises errors with no `server_version` and
  warns once. An `http_client` of your own wraps its transport in
  `paladin.RelaySyncTransport`.
- **Go: an RPC through `WithTLS` has no response-header timeout**, as without
  TLS; its context bounds it. Transfers keep theirs.
- **Go: `TLS.MinVersion`** raises the floor from TLS 1.2. After a rotation,
  connections on the old files close once idle.
- **The fakes complete without an ETag**, return a completed object on a
  repeat, and serve `EnsureTenantStorage`, as the server does.
- **A Python build without git** reads its version from a tag's source
  archive, or reports `0.0.0+unknown` instead of `0.0.0`.


## v0.14.0 — the Python SDK's transfers are declared once, stream and verify

The contract does not change. The Python SDK's `upload` and `download` now
match the Go SDK's, and break code written against 0.13:

- **Presigned requests go through a `Transfer`.** Pass one to
  `connect(…, transfer=paladin.Transfer(…))`; without it a shared default is
  used. They no longer go through `urllib`, so a proxy or TLS setting made for
  `urllib` no longer reaches them: give the `Transfer` a `transport`.
- **Redirects are refused.** A redirect from storage is now a
  `TransferError` with its 3xx status.
- **`TransferError(method, host, status, body)`** gains `host`, the host the
  request went to.
- **A whole download is verified**: `IntegrityError` when the size or the
  recorded checksum does not match. `download_stream` streams the content
  instead of returning it whole; `download` still returns bytes.
- **A single-PUT upload records its SHA-256** on the object.

## v0.13.0 — the Go SDK's transfers are declared once, stream and verify

The contract does not change. The Go SDK's `Upload` and `Download` do, and
break code written against 0.12:

- **The HTTP client for presigned requests moves to the client.**
  `UploadOptions.HTTPClient` and `Download`'s `httpClient` argument are gone.
  Declare it once with `paladin.WithTransfer(paladin.NewTransfer(
  paladin.WithTransferHTTPClient(c)))`; every `Upload` and `Download` through
  that client uses it.
- **`Download` returns an `*ObjectReader`.** `Download(ctx, data, name, nil)`
  becomes `Download(ctx, data, name, paladin.DownloadOptions{})`; the reader is
  the `io.ReadCloser` it returned, and `r.Object` the object.
- **A whole download is verified.** Its last `Read` returns an
  `*IntegrityError` rather than `io.EOF` when the size or the recorded
  checksum does not match. Code that treated any error at the end of the
  content as the end of the content now sees it.
- **Redirects are refused.** The default transport used to be
  `http.DefaultClient`, which follows them; a presigned URL never redirects
  legitimately, and following one sends its signed headers elsewhere. A
  redirect is now a `*TransferError` with its 3xx status.
- **A single-PUT upload records its SHA-256** on the object, through
  `CompleteObject`'s `checksum_value`.

## v0.12.0 — the SDKs sign in, connect every plane, and stamp idempotency keys

The contract gains one RPC, `EventSubscriptionService.RedriveFailedDeliveries`;
nothing in it changes. The SDKs change, and three of the changes break code
written against 0.11 (ADR-0018):

- **Every call with side effects carries an `Idempotency-Key`.** A unary call
  whose `idempotency_level` is unknown gets the context's key, else its
  request's `idempotency_key` field, else a fresh one, kept across retries.
  Each call without a key of its own gets a fresh one, so two calls are still
  two operations; only a key held across several calls — one context from
  `WithIdempotencyKey`, one `idempotency_key` block around a loop — answers
  every call after the first with the first response. Set a fresh key per
  logical operation. A call that must go out with no key at all takes
  `WithoutIdempotencyKey` / `no_idempotency_key`.
- **Such calls are now retried** on `Unavailable` and `ResourceExhausted`
  under `WithRetries`/`Retry`, since they carry a key; before, only calls
  made with an explicit key were.
- **A retry that cannot start before the deadline is not made**, and the
  server's error is returned rather than the context's. Code that matched
  `context.DeadlineExceeded` after a retried call now sees the server's code.

Added: `Session`/`AsyncSession` and `StaticToken`, `Connect`/`connect` with a
client for every service of each plane, `Pages`, `Wait`, `Mask`, `Upload`,
`Download`; `WithAPIToken`/`api_token`, `WithHeader`/`headers`, and a
`User-Agent` naming the SDK. The Python `Client` takes `token_source` and
`audience`. Retries wait a random share of the doubling ceiling and never less
than the server's `Retry-After`.

## v4.0.0, sdk/go/v0.11.0 — clients read the contract; the three prefix lists are gone

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
  de-duplication token, not a request to cache. Methods that mint a credential
  and are memoized (`CapabilityService/Issue`, `APITokenService/Create`,
  `UserService/ResetPassword`) store their response with the credential cleared
  and answer a repeated key with `AlreadyExists`.

If you integrate with Paladin, this is the rule to implement. `buf` puts
`idempotency_level` in every generated descriptor;
`@bufbuild/protobuf` exposes it as `method.idempotency`, and the Go runtime as
`MethodOptions.GetIdempotencyLevel()`.

## v4.0.0, sdk/go/v0.11.0 — 112 of 142 RPCs declare an idempotency_level

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

## v4.0.0, sdk/go/v0.11.0 — 103 of 142 RPCs declare an idempotency_level

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

## v4.0.0, sdk/go/v0.11.0 — 90 of 142 RPCs declare an idempotency_level

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

## v4.0.0, sdk/go/v0.11.0 — 36 read RPCs declare `idempotency_level = NO_SIDE_EFFECTS`

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

## v4.0.0, sdk/go/v0.11.0 — API tokens are addressed by resource name

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

## v4.0.0 — a failed operation fills `Operation.error`, not `Operation.response`

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

## v4.0.0, sdk/go/v0.11.0 — `force` is gone from the delete RPCs

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
