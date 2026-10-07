# BACKLOG

Living register of work that was **deliberately deferred**. Every entry
here corresponds to a decision recorded during a coding session: scope
trade-offs, missing metrics, downtime planning, or design questions
that need product input before implementation.

This file is the **single source of truth** for "what we noticed and
chose not to fix yet". Anything important that comes up in code review
should land here with a clear acceptance criterion before the PR
merges.

## Conventions

Each item carries:

- **Status** — `Deferred` (decision made, scope known) ·
  `Aspirational` (design exists, needs implementation) ·
  `Blocked` (waiting on external input / metrics) ·
  `In-Progress` (currently active in another branch).
- **Reason** — why it isn't done yet (one sentence).
- **Definition of Done** — the bar for moving it out of this file.
- **Blockers** — what changes the calculus.

When work lands that closes an item, **delete it** from this file in
the same commit. Do not leave "✅ done" markers — git history is the
audit trail.

When work lands that surfaces *new* deferred items, add them here in
the same commit. Treat this file like a runtime invariant.

---
## Deploy cutover: rename to `paladin`

### The `paladin` name collides — decide qualify-vs-rename before publishing

- **Status:** Open, not blocking. Registries checked 2026-08-21. Owner
  decision 2026-09-30: nobody else uses Paladin yet, so if the name turns out
  to be taken the service is renamed then; the repository goes public without
  waiting on it.
- **What the check found:**
  - **PyPI `paladin` is taken by NVIDIA** — `nv-paladin/paladin`, "The
    Foundation for All Paladin Libraries", Apache-2.0, 242 stars, version
    26.6.1 released 2026-06. This is the significant one: an active project
    from a large vendor, in adjacent infrastructure-software territory,
    holding the bare name on a major registry and a GitHub org (`nv-paladin`).
    We publish nothing to PyPI, so there is no packaging conflict today —
    but "Paladin" as a *project name* in infrastructure software is no longer
    unclaimed, and search results will not separate us.
  - **npm `paladin` is taken but abandoned** — v0.0.3, "a simple api
    framework", created 2014, last touched 2022, no repository, no licence,
    maintainer `mcfog`. Not a live conflict; also not available.
  - **The `@paladin` npm scope is free** (404), which is the obvious escape
    hatch if we ever publish JS.
  - **At least three unrelated 2026 packages use the name**:
    `@momidala/paladin` (v3.0.0, TypeScript MCP server), `paladin-mcp`,
    `paladin-verify`. The word is in active, uncoordinated use.
  - **pkg.go.dev has nothing** for `github.com/oleg-tkachuk/paladin` — the
    module is not resolvable while the repository is private, so the Go
    namespace is uncontested by default rather than by right.
- **Not checked:** trademark registers. The USPTO search API needs a key,
  and this is a legal question rather than a lookup — it wants a person, not
  a script. It is also the only one of these that applies whether or not we
  publish, because a public repository is use.
- **Definition of Done:** get the trademark question answered by someone
  qualified, then pick one and write it down here:
  1. **Keep `paladin` bare.** Lowest effort, permanent ambiguity with NVIDIA
     in every search.
  2. **Qualify it** — `paladin-cp`, `paladinctl`, or scope the JS side to
     `@paladin/*`. Cheap now, expensive after the repository is linked
     publicly.
  3. **Rename.** Only worth it if the trademark answer forces it.
  The cost of deciding late is in other people's bookmarks, not in this tree.
- **Blockers:** none. A rename is cheapest before anything is published
  under the name — the SDK distributions above all.

---

## MCP bridge

### Tool-coverage gaps vs the Paladin RPC surface

- **Status:** Deferred (partial — the data-plane cluster + admin read gaps
  landed 2026-06-26, budget / billing / platform-operation reads 2026-10-01).
  The remaining item below needs a product call.
- **Reason:** The MCP bridge (`internal/mcp/bridge.go`) exposes a curated
  subset of the ~110 Paladin RPCs. `DefaultCatalog` in
  `internal/mcp/profile.go` is the ground-truth list and is pinned to the
  real registrations by `TestServerRegistersDefaultCatalog`. Now wired:
  `UpdateObject`, `DeleteObjectTags`, `ListDistinctTags`, `BatchUpdateTags`,
  `RegenerateUploadUrl`, the 5 `MultipartUploadService` RPCs, data
  `CancelOperation`, admin `ResetUsage` / `GetAuditLogEntry` / `GetConfig`
  (`SystemService` client added). The budget, billing and platform-operation
  reads are wired too: `paladin_get_tenant_budget` and the platform-operation
  reads match `agent_safe`'s `get_*` / `list_*`, while the cross-tenant
  `paladin_budget_summary` and both billing tools are named outside those
  patterns so only the admin profile sees them. Budget `Set` and platform
  `CancelOperation` stay off the bridge: changing a tenant's spend cap or
  stopping a migration is an operator's call. The agent-usable upload/tag
  mutations are in `agent_safe`; `ResetUsage` / `system_config` / batch tools
  stay admin-only. UserSettingsService is not bridged on purpose — it holds a
  console user's display preferences, which mean nothing to an agent. What is
  still **not** reachable from an MCP agent, pending a product decision:
  - **Lifecycle writes** on Backend / Bucket / Collection / Tenant
    (create/update/delete) — Tenant lifecycle is intentionally human-only
    (denylist); the others are gaps if agent-driven provisioning is wanted.
  - **`RedriveFailedDeliveries`** — re-sends a subscription's failed events;
    an operator's call like the other subscription writes.
- **Definition of Done:** For each capability decided in-scope, add the tool
  in `registerReadTools`/`registerWriteTools`, append a `DefaultCatalog` row
  (the invariant test enforces this), and gate it into the right
  `DefaultProfiles` entry (destructive ops stay out of `agent_safe`). Wire any
  missing Connect client into `Clients`.
- **Blockers:** none technical. Needs a product call on whether an agent
  should drive provisioning (lifecycle writes) or leave it human-operated.

### Capability / API-token issuance via MCP — intentionally excluded

- **Status:** Won't-do (by design) unless a human-gated profile is added.
- **Reason:** `DefaultAlwaysDeny` blocks `paladin_capability_*` and
  `paladin_apitoken_*`: an agent minting/delegating/revoking its own capability
  or M2M token is a trivial bypass of the caveat model the agentic plane is
  built on. The bridge forwards `X-Paladin-Capability` but must never let the
  callee issue new authority. Recorded so the absence reads as a decision,
  not an oversight.
- **Definition of Done (only if revisited):** a separate, explicitly
  human-approved profile (not `agent_safe`/`admin`) that scopes capability
  issuance, plus an audit trail tying each issuance to the operator session.
- **Blockers:** security review; no current ask.

## Agentic plane / single-binary multi-mode migration

### Streaming RPCs through the inline transport

- **Status:** Deferred
- **Reason:** `internal/mcp/inline.go` routes Connect calls through
  `httptest.ResponseRecorder` — perfect for unary RPCs (which is all
  Paladin exposes today) but the recorder buffers the full response before
  the round-trip returns. A streaming RPC would deadlock waiting for
  EOF that never comes until the handler also finishes reading the
  request body.
- **Definition of Done:**
  - Replace the recorder-based RoundTripper with an `io.Pipe` pair
    plus a goroutine running the handler concurrently, draining
    request body and producing a streamed response on the fly.
  - Smoke test: a server-streaming RPC (when one is added) returns
    chunks before the handler completes.
- **Guard in place:** `TestInlineRoundTripper_BuffersFullResponse`
  (internal/mcp/inline_test.go) executably pins the current buffering
  behaviour — a client sees no bytes until the handler fully returns.
  It fails the moment the recorder is swapped for the `io.Pipe` pair,
  which is the signal to write the real streaming smoke test above.
- **Blockers:** No streaming RPC in the current proto surface. Land
  the first one (likely a `WatchEvents` for the agentic event bus)
  before this becomes load-bearing.

### KMS-wrapped capability signing key

- **Status:** Deferred
- **Reason:** Production deploys mount a PEM PKCS#8 file via Secret +
  RO volume. KMS-wrapped keys (AWS KMS, GCP KMS, Vault Transit) keep
  the private key from ever touching disk in cleartext — necessary for
  HIPAA / PCI / FedRAMP-tier compliance positioning the agentic plane
  spec calls out.
- **Definition of Done:**
  - `cfg.Capability.SigningKeyKMS` block with provider selector
    (aws-kms / gcp-kms / vault-transit) plus per-provider opts
    (key ARN / resource name / mount path).
  - `internal/capability/kms` package with KMS-backed Signer
    implementations. The Signer.Sign call emits a Sign-API request
    instead of holding the key locally.
  - SigningKeyPath stays as fallback for dev / on-prem deploys
    without KMS access.
- **Blockers:** none functional, but it's a compliance-driver feature;
  needs a customer ask before the KMS adapter implementations land.

### Presign and capability panels wait for those paths to run here

- **Status:** Deferred (no data to chart, deliberately).
- **Reason:** The Paladin dashboard and the five alert rules landed, and the
  collector is healthy again — `up{namespace="paladin"}` returns a series per
  pod and Grafana evaluates every rule with health=ok. Two of the signals the
  original entry named are still uncharted: `paladin_presign_total` and
  `paladin_capability_charges_total` exist in the binary but no presign or
  capability charge has run in this cluster, so a panel would show a flat zero
  that reads as "broken" rather than "unused". The alert on capability charges
  is written anyway, because a rule costs nothing while idle and a panel costs
  a wrong first impression.
- **Definition of Done:** Once either path runs here, chart it: presign by
  op and outcome with its p95, and capability charges by outcome. Read the
  label values off the live series first — that discipline is what the
  dashboard header records, and it has already caught a mistyped regex.
- **Blockers:** none. Needs traffic, not work.

### Role split: `scheduler` (extract cron-like triggers from worker)

- **Status:** Aspirational
- **Reason:** `worker` runs ALL background jobs today — lifecycle
  reaper (30m), refresh-token reaper (1h), capability/api_token
  reaper (1h), housekeeping (1h), replication (5m), lifecycle
  enforcement (30m). All compete for the same lease pool. As the
  job set grows, lease contention starts to dominate.
- **Definition of Done:**
  - New `serve scheduler` role: a single-replica process holding
    cron-style schedule definitions (interval-based) that emits
    "tick events" the worker pool picks up. Decouples "when to run"
    from "who runs it" — multiple worker replicas can race for the
    tick.
  - OR alternative: stay in-worker but split into multiple
    component-typed Deployments (e.g. `worker-reapers`,
    `worker-replication`) so resource limits don't spill across
    job classes.
- **Trigger to do:** when the job set grows past ~10 concurrent
  tasks, OR when one heavy job (replication) starts dominating
  shared CPU / DB connection budget, OR when an operator reports
  a reaper missing its window because replication held the lease.

### Role split: `indexer` / `embedder` (semantic search)

- **Status:** Aspirational
- **Reason:** No semantic-search feature today. When it lands
  (BACKLOG-aspirational), the embedder is fundamentally different
  from any existing role: heavy CPU/memory profile (LLM client
  calls + vector math), batched throughput pattern, separate
  egress to embedding API providers (Voyage / OpenAI / Cohere) or
  GPU access for local models.
- **Definition of Done:**
  - New `serve indexer` role: long-running consumer of object
    upload events, generates embeddings, writes to vector store
    (pgvector or external — Pinecone / Weaviate). Same observability
    contract as workers.
  - `cfg.Indexer` block: provider selector + per-provider options.
  - HPA tied to backlog-depth metric, not CPU.
- **Trigger to do:** when semantic search becomes a committed
  feature on the roadmap.

### Role split: `billing-aggregator`

- **Status:** Aspirational
- **Reason:** When per-tenant billing dashboard (BACKLOG step 5)
  lands, real-time aggregation queries (group by day × op × tenant
  across audit_log + capability_usage) become expensive on large
  tenants. A dedicated job that pre-computes daily snapshots into
  a `billing_snapshots` table makes the dashboard render fast and
  isolates analytical-query load from OLTP.
- **Definition of Done:**
  - New `serve billing` role (or fold into `worker` as one more job
    if scheduler split happens first): periodic aggregation pass
    over `audit_log` + `capability_usage` → daily snapshot rows.
  - `BillingService.GetTenantSnapshot(tenant, period)` → fast read
    of pre-computed totals.
  - Snapshot retention policy + reaper.
- **Trigger to do:** when the billing dashboard is built and a
  real tenant exceeds ~1M audit log rows per period, making
  on-the-fly aggregation slower than the SLA.

### Role split: `auth-server` (OAuth AS isolation)

- **Status:** Aspirational
- **Reason:** The OAuth 2.0 authorization-code flow has landed (ADR-0009,
  in the iam plane), so the authorization server now has its own attack
  surface (browser-facing /authorize, code storage, refresh
  rotation, JWKS rotation, client registration). Isolating from the
  iam plane lets you rotate signing keys without rolling api pods,
  apply a tighter NetworkPolicy on the OAuth endpoints, and run
  separate replicas for auth traffic vs request traffic.
- **Definition of Done:**
  - `serve auth-server` role hosting `/oauth/authorize`, `/token`,
    `/register`, `/.well-known/oauth-authorization-server`,
    `/.well-known/jwks.json`.
  - Independent JWKS rotation runbook.
  - api / admin verify tokens against the auth-server's JWKS — the same
    JWKS-verify path the planes already use.
- **Trigger to do:** when OAuth lands (separate BACKLOG entry) and
  becomes the primary auth path for at least one customer. Until
  then, fold OAuth endpoints into the existing iam plane.

### Role split: `realtime` (SSE / WebSocket subscriptions)

- **Status:** First consumer SHIPPED (2026-07-02) — the dedicated role stays
  trigger-gated on connection volume.
- **Shipped — live audit feed:** `013_audit_stream_notify.sql` NOTIFYs on
  every `audit_log` insert; `internal/auditstream.Hub` (one LISTEN connection per admin pod,
  per-tenant fan-out, slow consumers drop instead of back-pressuring) serves
  `GET /audit/stream` as bearer-authenticated SSE scoped to the JWT tenant.
  The UI consumes it through the BFF proxy (`/api/audit/stream`, session
  cookie → ExchangeAudience → pipe) with a Live toggle on `/audit`; SSE
  events are an invalidation signal (debounced ListAuditLog refresh), not a
  data source, so the full row incl. before/after keeps one code path.
- **Definition of Done (remaining — the actual role split):**
  - `serve realtime` role hosting the SSE/WebSocket handlers when long-lived
    connections stop belonging on the admin pod (restart latency drops every
    active stream; admin scales on RPC rate, not connection count).
  - Per-tenant connection limits.
  - Graceful shutdown draining connections instead of SIGKILLing them.
- **Trigger to do:** when concurrent stream connections grow past what an
  admin pod restart may reasonably drop (rule of thumb: >100 concurrent, or a
  second streaming surface — budget alerts / tool-call spectator — lands).

### Phase 5b.1 — Drop user-authn IAM, accept OIDC — WITHDRAWN

- **Status:** Won't-do (for now) — direction reversed 2026-06-30.
- **Decision:** Paladin is an **engineer-operated control plane** that
  integrates with other services and exposes an API for bucket access
  and management. Human authentication stays in Paladin's **own IAM** (local
  `users` + HS256, and the OAuth Authorization Server in
  [ADR-0009](docs/adr/0009-oauth-authorization-server.md) = Paladin IAM
  itself); service-to-service is `api_keys` + capabilities. An external /
  federated IdP is **not needed now**, so the earlier premise — "user
  authn is a B2B anti-feature, offload to the customer's Okta / Auth0 /
  Cognito / Keycloak" — is **withdrawn**. The local `users` /
  `refresh_tokens` tables and the `auth_service` / `user_service` /
  `user_settings_service` are **kept**, not removed.
- **Reconsider only if:** a concrete customer mandates SSO against their
  own IdP. The swap stays cheap — the OAuth RS/AS halves
  ([ADR-0008](docs/adr/0008-mcp-oauth-resource-server.md) /
  [ADR-0009](docs/adr/0009-oauth-authorization-server.md)) are
  IdP-agnostic by design, and an inert JWKS verifier stub already exists
  (see *Federated IdP via JWKS* below). Until such an ask, this is not on
  the roadmap.

---

## Production-readiness audit (2026-06-12)

_Full security / data-integrity / ops sweep. The 7 BLOCKER findings and
most HIGH/MEDIUM items were fixed in this work stream — see git history
(login rate-limiter, bounded CEL+JWKS caches, presign TTL clamp, IAM
audit interceptor, idempotency purger + ON CONFLICT, multipart reaper,
ListObjects pagination, frontend CSP/HSTS/CSRF + liveness, MCP PDB,
sticky sessions, ingest dedup pool, tenant-delete precheck, AsyncWriter
shutdown, composeKey guard). The entries that remain below were kept
open deliberately — each notes why._

### Federated IdP via JWKS

- **Status:** Won't-do (for now) — not on the roadmap (2026-06-30).
- **Reason:** Paladin is engineer-operated and owns its own IAM (see the
  withdrawn *Phase 5b.1* above), so accepting tokens minted by an
  external IdP is **not needed now**. `auth.jwks_url` switches the planes
  (`internal/app/build_listeners_common.go`) and the MCP edge
  (`cmd/server/serve_mcp.go`) to `auth.NewJWKSVerifier`, but nothing sets it:
  every JWT today is HS256 minted by the IAM plane itself.
- **Reconsider only if:** a customer mandates SSO against their own IdP.
  Then finish the verifier (cache + rotation grace window, required
  `kid` matched against the active key set, role-claim mapping such as
  `cognito:groups` → Paladin roles) plus an end-to-end test issuing a token
  from a mock OAuth2 provider and verifying it through the data plane.

### Audit-log encryption at rest beyond filesystem-level

- **Status:** Deferred
- **Reason:** `audit_log.before_json` / `after_json` are BYTEA. They
  can carry policy diffs, secret refs, and PII-bearing tenant labels.
  Today they're protected only by Postgres filesystem-level encryption
  (whatever the deploy storage provides).
- **Definition of Done:**
  - Per-tenant data-encryption keys (DEK) wrapped by a KMS-managed
    key (KEK).
  - Audit middleware encrypts `before_json` / `after_json` with the
    tenant DEK before insert.
  - Read path decrypts on `ListAuditLog` / `ExportAuditLog`.
  - DEK rotation runbook.
- **Blockers:** KMS choice (AWS KMS vs Vault Transit vs cloud-agnostic
  envelope-encryption library).

---

## Dependencies

### Take grpc to the stable release carrying the GO-2026-6443 fix

- **Status:** Open — waiting on upstream. There is nothing to bump to yet.
- **Reason:** `govulncheck` reports GO-2026-6443 against
`google.golang.org/grpc@v1.84.0`, which is the latest STABLE release
(2026-09-17). The advisory names the fix as
`v1.85.0-dev.0.20260825072537-93e31b48545e`, and the proxy lists only
`v1.85.0-dev` and `v1.86.0-dev` above ours — both pre-releases, which this
project does not adopt.
`sec:vuln` is green today and honestly so: our code does not reach the
vulnerable path, so the finding sits in "packages you import" rather than
"your code is affected". That is the whole of the protection — it holds
because of what we do not call, not because of what we do not depend on, and
a future change that reaches that path turns the gate red with no dependency
having moved.
- **Definition of Done:** `google.golang.org/grpc` at the first stable release
that carries the fix, `task -t Taskfile.dev.yaml sec:vuln` still green, and
this entry deleted.
- **Blockers:** upstream has not cut a stable v1.85.0. `task -t
Taskfile.dev.yaml deps:update` will not pick a pre-release, so this does not
resolve itself on the next dependency bump — it needs someone to notice the
release.

### `golang.org/x/crypto` GO-2026-5932 has no fix upstream

- **Status:** Open, and not actionable — recorded so it is not re-discovered.
- **Reason:** `govulncheck` reports GO-2026-5932 against
`golang.org/x/crypto@v0.57.0` with **Fixed in: N/A**. Unlike the grpc one
above, no release resolves this: bumping the dependency cannot help until
upstream ships something.
Unreachable from our code today, so `sec:vuln` stays green.
- **Definition of Done:** upstream publishes a fixed version, we take it, and
this entry goes. If the advisory is instead withdrawn or re-scored, delete it
with a note in the commit saying which.
- **Blockers:** upstream. Worth re-checking whenever `sec:vuln` output is read
rather than on a schedule — the first sign of it mattering would be the
finding moving from "packages you import" to "your code is affected".

## Performance / Scale

### Per-table autovacuum tuning

- **Status:** Blocked
- **Reason:** [the consolidated baseline](backend/migrations/001_initial_schema.sql) (was migration 008 before consolidation)
  set fillfactor on hot-update tables but didn't touch
  `autovacuum_vacuum_scale_factor` / `autovacuum_analyze_scale_factor`
  per table — needs live bloat metrics to choose values that aren't
  guesses.
- **Definition of Done:**
  - 30 days of `pgstattuple` / `pg_stat_user_tables` data.
  - Per-table override for the 5 hottest tables landing in a follow-up
    migration with measured before/after bloat.
- **Blockers:** observability instrumentation + a representative
  workload.

---

## Features

### Public collections: the console cannot create or show them

- **Status:** Deferred
- **Reason:** public buckets and collections ([ADR-0027](docs/adr/0027-public-collections.md))
  are created through the admin API only; the console neither offers the
  settings nor marks a bucket or collection as public.
- **Definition of Done:** the bucket and collection create dialogs offer
  public read (the bucket's only on a backend whose probe found
  `ANONYMOUS_READ_POLICY` supported, saying why otherwise), and the lists and
  detail pages mark what is public and show `public_base_url`; covered by
  component tests.
- **Blockers:** none.

### Public collections on a store without bucket policies

- **Status:** Aspirational
- **Reason:** a public bucket is opened with an anonymous-read bucket policy,
  so a store that has none — Garage among them — cannot host one, and the
  probe says so. Garage serves a whole bucket unsigned through its website
  endpoint instead.
- **Definition of Done:** a second mechanism for such stores (Garage website
  mode on its own host), chosen by a probed feature, with the public URL built
  for it and an integration test against Garage.
- **Blockers:** whether any deployment publishes from Garage.

### The compose stack's SeaweedFS serves every request unsigned

- **Status:** Deferred
- **Reason:** `backend/deploy/seaweedfs-s3.json` gives Paladin's credentials to
  SeaweedFS' `anonymous` identity, which grants its actions to unsigned
  requests too. Nothing in the stack is private at the store, and the feature
  probe reports `ANONYMOUS_READ_POLICY` unsupported there, so public
  collections cannot be tried on it.
- **Definition of Done:** the compose stack uses a named identity, as the
  integration suites and the e2e stack do, and its e2e run still passes.
- **Blockers:** none; check that nothing in the dev stack reads storage
  unsigned.

### Anonymous reads of public objects are not counted

- **Status:** Deferred
- **Reason:** a public object is read straight from the store or a CDN, so no
  Paladin process sees the read: egress is attributed to no tenant and appears
  in no Paladin metric ([ADR-0027](docs/adr/0027-public-collections.md)).
- **Definition of Done:** if egress per tenant matters, ingest the store's or
  the CDN's access logs and attribute them by the tenant segment of the path;
  otherwise delete this entry with that decision recorded.
- **Blockers:** whether per-tenant egress is ever billed.

### A storage backend's feature probe runs only on request

- **Status:** Deferred
- **Reason:** `TestBackend` is the only thing that probes a backend's S3
  features ([ADR-0026](docs/adr/0026-storage-backend-features-are-probed.md)).
  A backend nobody has tested shows every feature unknown, and a result goes
  stale when the store is upgraded or reconfigured, with nothing re-checking
  it.
- **Definition of Done:** the worker probes every enabled backend at start and
  on an interval, records the results the way `TestBackend` does, and the
  console shows how old each result is; a test drives the worker against the
  probe store.
- **Blockers:** none; the interval and whether a probe's scratch bucket is
  acceptable on a schedule for every store want a decision.

### Replication: real `StorageReplicator` implementation

- **Status:** Aspirational
- **Reason:** [internal/worker/replication.go](backend/internal/worker/replication.go)
  walks replicated buckets and logs intent; [internal/app/build_jobs.go](backend/internal/app/build_jobs.go)
  injects `Replicator: nil` so the worker is dry-run only, and every
  AVAILABLE object is selected — the CEL `replication.filter` is not
  evaluated yet.
- **Definition of Done:**
  - `StorageReplicator` impl that performs cross-backend `CopyObject`
    via S3 SDK (AWS-native CRR for same-account, manual stream-copy
    otherwise).
  - Watermark advance + retry-with-exponential-backoff on transient
    errors.
  - Evaluate `replication.filter` (CEL) when selecting objects.
  - Integration test across two backends — the suites bring up two
    SeaweedFS instances for exactly this.
- **Blockers:** scope decision — same-cloud only vs. cross-cloud.

### Lifecycle transition rules are not applied

- **Status:** Deferred
- **Reason:** [internal/worker/lifecycle.go](backend/internal/worker/lifecycle.go)
  applies `Expiration` only; a rule's transition (storage-class change) is
  accepted and stored but has no effect until it maps onto the backend's own
  S3 lifecycle configuration.
- **Definition of Done:** transitions either drive the backend's lifecycle
  configuration or are refused at write time, with tests for both the rule
  validation and the worker.
- **Blockers:** which backends expose storage classes worth transitioning to.

### Storage-ingest delete events are only logged

- **Status:** Deferred
- **Reason:** [internal/eventingest/handler.go](backend/internal/eventingest/handler.go)
  promotes on an upload event but logs a delete event and returns; an object
  deleted straight from the bucket keeps its AVAILABLE row until the
  reconciler finds the bytes missing.
- **Definition of Done:** a delete event soft-deletes the matching object row
  through the state machine (idempotent on redelivery), with handler and
  integration tests.
- **Blockers:** none; needs the delete-cascade semantics in
  [docs/deletion-semantics.md](docs/deletion-semantics.md) applied to this path.

### `Object.placement` is never populated

- **Status:** Deferred
- **Reason:** `PhysicalPlacement` (backend, bucket, storage path) exists in
  `proto/paladin/data/v1/types.proto`, but
  [connectshim/data/conv.go](backend/internal/api/connectshim/data/conv.go)
  leaves it unset: it should reach only privileged callers, and no role check
  for that exists on the data plane.
- **Definition of Done:** a role (or Cedar action) that gates placement, the
  field set for callers holding it, and tests for both sides of the gate.
- **Blockers:** deciding who counts as privileged on the data plane.

### Console `/users`: no user detail page, no scope grants

- **Status:** Deferred
- **Reason:** `/users` lists users across tenants, but there is no per-user
  page and `UserService.GrantScopes` / `RevokeScopes` are not reachable from
  the console; an operator changes scopes through the API or an SDK.
- **Definition of Done:** a user detail page showing roles and scopes, with
  grant and revoke actions, and page tests.
- **Blockers:** none.

### `ResetPassword` — self-service email delivery

- **Status:** Won't-do (2026-06-30) — out of scope by product direction.
- **Reason:** Paladin is an **engineer-operated** service: its `users` are
  operators, not end-customers, so password resets are an operational task,
  not a self-service flow. An admin already calls
  [userh/handler.go](backend/internal/api/iam/v1/userh/handler.go) `ResetPassword`,
  which returns the new password to hand off out-of-band — that is the
  intended model and it is sufficient. Same direction as withdrawing the
  external/federated IdP: human auth stays Paladin's own IAM, kept deliberately
  minimal. Wiring an email-link self-service flow (and an email sender) would
  add surface area Paladin's audience doesn't need.

### Event dispatcher: producer wiring — handler-class integration tests + adoption

- **Status:** Deferred (parent SHIPPED — only follow-ups remain)
- **State as of 2026-05-10:** Producer wiring complete across
  every handler class that today's customer surface needs:
    - **Lifecycle classes** (always-on): tenant / bucket /
      objectKey / quota / object. Pattern in
      `internal/api/admin/v1/tenanth/handler.go::EventProducer`,
      reused across the four sibling handlers; admin pod
      wiring in `build_listeners_admin.go`, api pod wiring
      in `build_listeners_api.go::apiDispatcher`. Event
      classes: `paladin.{tenant,bucket,object_key,quota,object}.*`.
    - **Cross-cutting classes** (opt-in): capability charge
      + audit-log mirror, both gated on cfg.Dispatcher
      toggles (`charge_events_enabled` /
      `audit_mirror_enabled`, default OFF). Adapters in
      `internal/app/{charge_emitter,audit_mirror}.go`
      bridge `*worker.Dispatcher` to
      `auth.ChargeEventEmitter` and
      `middleware.AuditMirrorEmitter`. Event classes:
      `paladin.capability.charged` and `paladin.audit.<action>`.
    - **Not applicable:** `policy` event class — the data-
      plane policy handler is read-only; Cedar policy
      mutations go through parent `*.updated` events.
- **What's left:**
  - ~~Integration tests for bucket / object_key / quota / object lifecycle
    handlers.~~ **DONE (2026-06-29):**
    `tests/integration/lifecycle_events_test.go` pins all four seams
    end-to-end (handler write → outbox row → dispatcher tick → CloudEvents
    envelope on NATS): bucket→`paladin.bucket.updated`,
    object_key→`paladin.object_key.updated`, quota→`paladin.quota.set`,
    object→`paladin.object.updated`. Writing the quota test surfaced + fixed a
    real bug — the tenant/bucket-scoped quota upserts used `ON CONFLICT
    (col)` against a **partial** unique index, so every `SetQuota` failed
    with 42P10; added the `WHERE … IS NOT NULL` index predicate to
    `queries/quotas.sql` (+ sqlc regen). Also un-bit-rotted the
    `tests/integration` package (stale `NewBackendRepoV2` / `NewBucketRepoV2`
    call sites — the suite is `//go:build integration`, outside the default
    gate, so it had drifted and stopped compiling).
  - ~~Tests for the cross-cutting emitter classes (capability charge +
    audit-log mirror).~~ **DONE (2026-06-30):** introduced a small
    `eventDispatcher` interface seam in `internal/app` (both emitters now
    depend on it instead of the concrete `*worker.Dispatcher`) and added
    `charge_emitter_test.go` + `audit_mirror_test.go`. They pin the producer
    contract with a fake dispatcher — event Type/ResourceName/Payload mapping
    (`paladin.capability.charged`, `paladin.audit.<action>`), the tenant-less drop
    guards, best-effort error swallowing, the `auditEventType` action→class
    derivation (incl. degenerate inputs), and the nil/`optionalAuditMirror`
    toggle gating. The dispatcher → outbox → NATS transport itself stays
    covered by `lifecycle_events_test.go`, so this is the right granularity —
    no redundant integration spin-up.
  - **Adoption check** — once a real subscriber needs charge
    or audit events, flip `cfg.Dispatcher.ChargeEventsEnabled`
    / `audit_mirror_enabled` per overlay and verify the
    expected fan-out volume against operator expectations.
    Both default OFF; subscribers MUST add a CEL filter on
    their EventSubscription so a noisy tenant doesn't drown
    the dispatcher's outbox loop.
- **Trigger to do:** any of —
    - Adding a 6th lifecycle handler that follows the same
      pattern (e.g. object_tags, when its lifecycle becomes
      first-class).
    - First customer that flips charge / audit events on —
      validate the cardinality assumption holds for their
      tenant.

### Event dispatcher: per-tenant Kafka topic prefix

- **Status:** Deferred — the Kafka sink, its SASL/SCRAM and mTLS auth, secret
  refs and real-broker tests (`tests/integration/components/broker_sinks_test.go`)
  have shipped; the topic is operator-defined per subscription.
- **Reason:** nobody has asked for automatic fan-out by tenant.
- **Definition of Done:** an optional per-tenant topic prefix on `KafkaSink`,
  with the delivery and pool-key tests that cover it.
- **Blockers:** a customer whose consumers need one topic per tenant.

### The Python SDK is not published

- **Status:** Deferred (owner decision, 2026-09-30).
- **Reason:** there are no users outside the owner, so the SDK is installed
  from the repository. The distribution name `paladin-sdk` is provisional and
  follows the product name if that changes.
- **Definition of Done:** a publish step in `.github/workflows/release.yaml`
  after the SDK tag is cut (PyPI trusted publishing, a `pypi` environment);
  the version already comes from the `sdk/go/v*` tag through hatch-vcs.
- **Blockers:** the decision to publish.

### Terminal tenant events are observed by query, not by subscription

- **Status:** Documented 2026-08-28, not a gap. Supersedes an entry that asked
  for platform-scoped subscriptions.
- **Reason:** PurgeTenant no longer attempts a `paladin.tenant.purged` fan-out;
  the outbox cannot carry it, since the delivery row and the subscription that
  would receive it both cascade from the tenant being removed. The earlier
  entry read that as a missing capability and proposed a new one — platform-
  scoped subscriptions, or a nullable tenant reference on deliveries.
  Both solve the wrong problem. `audit_log` has NO foreign key to `tenants`,
  survives the purge, and already records it: 522 `PurgeTenant` entries in the
  dev cluster at the time of writing, alongside 1809 `DeleteTenant`. The
  platform-level record exists, outlives the tenant, and has an access model.
  The event system is tenant-scoped by design — subject and audience are the
  same tenant — and a terminal event has no tenant audience by definition.
  Adding a platform subscription class would be a SECOND platform-observation
  mechanism beside a working one, and an expensive one: subscribing across
  tenants is the right to watch every tenant's lifecycle, with its own authz,
  filters, retries and audit.
- **Definition of Done:** nothing here. One mechanism per concern — tenant-level
  push is events, platform-level observation is the audit log.
- **If a consumer genuinely needs push** rather than a query, the ask is
  "stream the audit log", which is a general capability and a different item.
  It is not a special case of tenant events, and should not be built as one.

### Streaming RPCs are charged one rate-limit token at open

- **Status:** Not reachable today, re-checked 2026-08-30. The API declares
  ZERO server-streaming RPCs — `rpc … returns (stream …)` appears in no proto
  — so WrapStreamingHandler never runs for a real method. Event subscriptions,
  which the reason below cites as "the streams Paladin has", are delivered by
  the dispatcher over webhook and NATS, not over a streaming RPC; the SSE
  audit feed is a plain handler on the admin mux and never passes through the
  Connect interceptor chain at all. Keep the entry: the hole is real the day a
  streaming RPC is added, and the interceptor will still charge one token.
- **Reason:** `TenantRateLimitInterceptor.WrapStreamingHandler` bumps the
  tenant's window once when the stream opens and never per message. That fits the streams
  Paladin has — event subscriptions, whose cost is the subscription rather than
  the frame — but a tenant can hold a stream open and push messages through it
  at any rate without the limiter noticing, so the ceiling covers unary traffic
  only.
- **Definition of Done:** Per-message accounting inside the message loop for
  any stream whose per-frame cost is non-trivial, or a documented statement
  that streams are governed by their own concurrency limit instead.
- **Blockers:** none. No stream in the API today carries enough per-frame work
  to be worth the accounting.

### A reclaimed operation records a count, not which items landed

- **Status:** Deferred (needs per-executor progress semantics).
- **Reason:** `StaleOperationReclaimer` now copies the runner's last
  `{processed, total}` snapshot into the failure payload as `last_progress`,
  so a caller learns the batch reached at least item N. That is a count, not
  an identity: the batch executors are not transactional across their items
  and process them in argument order, so "7 of 9" only implies which items
  landed as long as the executor never reorders. Nothing enforces that today,
  and the snapshot is throttled to ~1/s, so the true figure is at least N.
- **Definition of Done:** A reclaimed operation's response carries the
  per-item outcomes the successful path already returns, so a caller can
  reissue exactly the remainder rather than inferring it from a count.
- **Blockers:** none technical. Still deferred because retrying the whole
  batch is correct for the idempotent executors (tags, restore) and only
  wasteful, and the two where it is not (copy, permanent delete) deserve a
  design pass — most likely per-item rows the executor writes as it goes —
  rather than a partial record bolted onto the reclaim.

---

## Database

### Index candidates considered and rejected (2026-08-18 audit)

- **Status:** Deferred — decisions recorded so the audit is not repeated from
  scratch. Pre-consolidation migrations 064–068 added the five indexes this pass judged worth
  their write cost; these are the ones it did not.
- **Reason (per candidate):**
  - **`objects (tenant_id, state) INCLUDE (size_bytes)`** — would turn the
    /stats object census and the quota reconciler's recompute into index-only
    scans. Rejected: `state` is UPDATEd on every PENDING→AVAILABLE promote and
    every delete, so the index makes the hottest write path's UPDATEs non-HOT,
    working directly against the `objects` `fillfactor` tuning in
    `001_initial_schema.sql`, applied for
    that exact reason. Paying on every upload to speed a 15-minute background
    job and a dashboard poll is the wrong trade.
  - **`audit_log (at DESC, entry_id DESC)`** — ListAuditEntries pages by that
    composite keyset while `idx_audit_log_at` covers only `at`. Rejected: ties
    on `at` are rare, audit_log is partitioned and already carries six
    indexes, and every Paladin mutation writes a row — a seventh index taxes the
    write path of the whole control plane for a tiebreak.
  - **`objects (tenant_id, object_key, key text_pattern_ops)`** — would make
    ListObjects' `key LIKE 'prefix%'` a range scan instead of a recheck.
    Rejected for now: the `(tenant_id, object_key)` equality already bounds
    the scan to one collection, so the recheck is over a page, not the table.
    Revisit if a single collection grows large enough that prefix browsing
    shows up in `pg_stat_statements`.
- **Definition of Done:** revisit each when there is production evidence —
  `pg_stat_statements` mean_exec_time, or `pg_stat_user_tables` seq_scan
  counts — rather than on inspection.
- **Blockers:** none; needs a real fleet to measure against.

### connectshim/iam: exhausted, with three equivalent mutants left

- **Status:** Deferred (the seam and the tests landed; this entry records why
  the score stops at 96%).
- **What landed:** an interface per handler-backed server — Auth, User,
  UserSettings; SystemServer has no handler — a table over all 21 RPCs
  asserting a handler failure reaches the caller wrapping the original, plus
  cases for the parsing, forwarding and SUCCESS paths a failing double cannot
  reach. 78% after the seam alone, 96% after those.
- **Not a sample, unlike the admin entry above:** a budget of 90 produced only
  70 mutations, so the operator set is exhausted. Every non-equivalent
  mutation in this package is caught.
- **The three that remain, each proved equivalent by running the mutated
  predicate against its boundary input rather than by argument:**
  `parseUserResourceName`'s `len(name) <= len(prefix)` and
  `parseTenantParent`'s `len(parent) <= len(prefix)` (at exactly the prefix
  length the mutant falls through to a uuid.Parse of the empty string, which
  fails the same way), and `indexOf(body, "/settings") >= 0` (at index 0 the
  untruncated body fails the same parse). All three differ only in the error
  MESSAGE; a test that pinned the prose would be brittle and would hold no
  behaviour.
- **Blockers:** none.

### Mutation testing: how to read what it says

- **Status:** Deferred (the tool is in the repo; this is the note that goes
  with it).
- **What exists:** `backend/scripts/mutate.py`, run as
  `task backend:test:mutate -- <pkg> <glob> <budget> [--test-cmd …]`. Not part
  of any gate: it rewrites source in place, takes minutes per package, and its
  output is a list of questions rather than a pass or a fail.
- **The denominator is the argument that matters.** Running a package's own
  tests is the wrong measure for anything DB-backed.
  `internal/capability/postgres` showed 8 survivors and the one checked by hand
  is caught by `tests/integration/components`. Reporting those as gaps sends someone to
  write tests for behaviour that is already held. `--test-cmd` exists for that,
  and a filter that matches the wrong test names produces a 0% which is also
  not a finding.
- **This repo has TWO integration suites,** `tests/integration/components` and
  `tests/integration`, and the dispatcher's tests are in the second. A
  `--test-cmd` naming only the first reports branches as unheld that the other
  suite covers. Name both.
- **A sampled score says nothing about a line.** connectshim/admin scored 98%
  on an eighty-mutation sample and 86% exhaustively: 43 survivors the sample
  never drew. To retire a named survivor, mutate that line; to call a package
  done, give the run a budget above its mutation count, which the summary
  line reports.
- **A survivor is a question, not a defect.** The useful ones are guards whose
  whole purpose is to refuse something — that is where this found the JWT
  verifier with no test file, the unauthenticated allow-list with none, and the
  validation interceptor with none.
- **Line numbers shift when a file is edited.** Comparing "line 72 survived"
  across two runs of a file changed in between compares different lines. Take a
  score from one pass.
- **Blockers:** none. It is a tool, not scheduled work.

### Index-usage tests are pinned to measured table sizes

- **Status:** Deferred (works today; a trap for later).
- **Reason:** `tests/integration/components/index_usage_test.go` proves the planner
  *chooses* each new index rather than merely being able to. That makes the
  tests sensitive to seed size: the operations keyset index is not chosen
  below roughly 8k rows / 40 tenants (the primary key is a UUIDv7, so it
  already yields creation order and wins on a small table), and the objects
  keyset index is not exercised meaningfully unless rows span several
  ObjectKeys. Both tests seed past the measured crossover and say so, but the
  numbers are empirical and could drift with a Postgres version bump or a
  cost-setting change.
- **Definition of Done:** if one of these starts failing after an upgrade,
  re-measure the crossover before assuming the index became useless — the
  probe is a few lines of EXPLAIN over a scaled seed, and the failure message
  prints the plan that replaced it.
- **Blockers:** none.

### List filters push down only the conjuncts SQL can express

- **Status:** Deferred, narrowed 2026-08-28 (timestamps), 2026-10-05
  (operation `state` and `done`) and 2026-10-06 (value sets).
- **Reason:** The filterable list RPCs extract the SQL-expressible subset of
  the caller's CEL (`cel.ExtractPushdown`) and hand it to the query, so
  `filter` selects from the table rather than from whichever page the cursor
  landed on. The walk understands the top-level `&&` chain of string equality,
  `startsWith`, `contains`, booleans, and — as of 2026-08-28 — `created_at`
  ranges, threaded into all seven list queries. As of 2026-10-06 a string
  field's equality is a set of values, pushed as `= ANY(…)`: `field in [...]`
  and a disjunction of equalities on that one field reach the table too, in
  the six queries the generic walk serves (objects and audit keep their own
  walkers, and the object walk already split disjunctions into branches). Strict `>` / `<` are widened
  to their inclusive forms deliberately: the pushdown may only narrow, so an
  extra boundary row is free and a missing one is a wrong answer.
  What still reaches only the in-memory pass: disjunctions across fields or
  over anything but equalities, `labels[…]`, functions, and `updated_at` — which is left out on purpose rather than
  forgotten, since a mutable column pushed into the query can exclude a row
  that the CEL pass, running microseconds later against a row someone just
  touched, would have accepted. A filter made entirely of those reads the
  whole table one page at a time: correct (paging continues, no row is
  dropped) and slow.
  One path is narrower still: `ListCollections` has a hand-written branch for
  the (backend, bucket) browser that takes only the `search` hint.
- **Definition of Done:** Either the walk covers the rest of the CEL surface
  each schema exposes, or the schemas stop exposing what no query can answer.
  Whatever is added must hold the invariant
  `TestPushdownNeverExcludesARowTheFilterAccepts` states: SQL may over-fetch,
  never under-fetch.
- **Blockers:** none. Deliberately not solved by rejecting un-pushable filters
  with InvalidArgument: that would make a legal CEL expression an error
  because of an implementation detail of one storage engine, and the shape the
  object and audit paths established is narrow-only for exactly that reason.

## Configuration

### MCP `allow_write` was a dead knob that read as a security control

- **Status:** Deferred (schema entry removed; the question it raises is open).
- **Reason:** `schema.cue` declared `mcp.stdio.allow_write` and
  `mcp.http.allow_write`, both defaulting to `false`. Neither had a Go field
  or a single consumer — the value was computed by CUE and dropped on decode.
  An operator reading the schema would reasonably conclude the MCP bridge was
  read-only by default and that flipping the knob was what enabled writes.
  Neither is true. Worse, the strict loader rejects unknown keys, so actually
  setting it in a config file crashes the pod. The schema entries are gone;
  what is NOT resolved is whether the bridge should have such a gate.
- **Definition of Done:**
  - A decision, written down: either the MCP bridge grows a real read-only
    mode (field + enforcement in internal/mcp/bridge.go + tests), or
    docs/security.md states plainly that MCP write access is governed only by
    the caller's token scopes and Cedar policy.
  - If it becomes real, the config knob comes back — with the enforcement,
    not before it.
- **Blockers:** needs a product/security call on whether a transport-level
  write gate adds anything over the existing per-call authorization.

### `llm:` and `vector:` schema blocks removed — features never landed

- **Status:** Deferred (schema cleaned; reinstate with the implementation).
- **Reason:** `schema.cue` carried fully-specified `llm:` (litellm + ollama)
  and `vector:` (pgvector + qdrant) blocks with defaults, secret refs and
  timeouts. No Go struct, no yaml tag, no consumer anywhere in the tree — and
  because the loader is strict, an operator who followed the schema and set
  `llm.enabled: true` would fail config load and crashloop the pod. A schema
  block for an unimplemented feature is not a placeholder, it is a trap.
  Removed; the CUE is recoverable from git history when the work starts.
  The shape they described: `llm` selected between a LiteLLM gateway and a
  local Ollama, with per-binding `{provider, model}` entries; `vector` chose
  between pgvector and Qdrant at 1536 dimensions. That is the config surface
  the aspirational **Role split: `indexer` / `embedder`** entry above would
  need — so this is a schema that ran ahead of its feature by a release or
  more, not a mistake.
- **Definition of Done:** the blocks return in the same change that adds the
  Go structs and the code that reads them, not before. When that happens,
  reconcile the naming with that entry's `cfg.Indexer` block — two different
  names for the same subsystem is how this drift starts.
- **Blockers:** none — gated on semantic search becoming a committed feature.

## UI / Admin Console

### Option: Inter and IBM Plex Mono as the console's typefaces

- **Status:** Deferred (an option, decided against for now — Geist stays).
- **Reason:** the ember palette was designed alongside Inter for text and IBM
  Plex Mono for identifiers and figures; the console sets both in Geist and
  Geist Mono. The colours carried over without the type, which is a choice,
  not an oversight. Recorded so the option is not re-discovered as a gap.
- **Definition of Done (if taken up):** both faces self-hosted from npm
  packages (`@fontsource-variable/inter`, `@fontsource/ibm-plex-mono`) rather
  than `next/font/google`, which fetches at build time and would break the
  air-gapped build `src/app/layout.tsx` is written to keep; exposed under the
  existing `--font-sans` / `--font-mono` variables so no component changes;
  tabular figures checked in the tables that align counts; per theme or for
  all, decided when it is taken up.
- **Blockers:** none — a product decision.

### Platform Stats: no cached rollup — the object census is a live GROUP BY

- **Status:** Deferred (correct at current scale; revisit on fleet growth).
- **Reason:** `/stats` computes the per-tenant/per-state object census with a
  single `GROUP BY (tenant_id, state)` over `objects`, run on the worker's
  BYPASSRLS pool on every poll (30s while the tab is visible). The four
  sibling censuses beside it are cheap counts over small tables; this one is
  the only O(rows) query in the set. At today's row
  counts that is a cheap index-less aggregate, and a live number is strictly
  more useful to an operator than a stale one. It does NOT stay cheap: the scan
  is O(rows), so a fleet in the tens of millions of objects turns a dashboard
  poll into a recurring seq-scan. Deliberately not pre-optimised — a
  materialized rollup is a correctness surface (staleness window, refresh
  scheduling, backfill on restore) that shouldn't be paid for before the scan
  actually hurts.
- **Sequencing (decided):** do NOT introduce a second denormalised counter for
  this. The repo already had one — `quotas.usage_*` — and it drifted into
  uselessness precisely because the reconciler was assumed rather than
  written. `worker.QuotaReconciler` now exists and already performs the exact
  rollup this entry wants (per-tenant bytes + object count from live rows) on
  a schedule. When the census scan starts to hurt, the move is to persist that
  job's intermediate result and have `/stats` read it — one mechanism, two
  consumers, one source of truth — not to add a parallel summary table.
- **Definition of Done:**
  - First and cheapest: a covering index on `objects (tenant_id, state)
    INCLUDE (size_bytes)` to turn the seq scan into an index-only scan.
    Measure both sides before committing — `state` is a key column and it is
    UPDATEd on every promote, so the index makes those writes non-HOT, which
    works against the deliberate `fillfactor` tuning in `001_initial_schema.sql`.
  - Only if that is not enough: persist the reconciler's per-tenant rollup and
    read it from `/stats`, with the staleness window shown in the UI ("as of"
    already has the slot).
  - A measurement in the runbook that says when to switch: a p95 for the
    aggregate from `pg_stat_statements`, not a row-count guess. Suggested
    trigger: mean_exec_time > 1s.
- **Blockers:** none — needs a real fleet to measure against. Note the load is
  on-demand, not background: the page polls only while an operator has it open
  in a visible tab.

### Announce that bucket and per-day quota caps now reject

- **Status:** Blocked (operator action — a coding session cannot send the
  announcement).
- **Reason:** bucket caps were never enforced: the rows sat under the tenant
  RLS policy with `tenant_id` NULL, invisible to the upload check, and their
  daily counters were never charged. Since `044_bucket_quotas.sql` they live in
  `bucket_quotas`, are charged on every promote and are enforced. Per-day caps
  on tenant quotas are enforced too. Anyone who set one of these caps while it
  was inert has a live rejection waiting.
- **Definition of Done:**
  - Run the over-cap query below against each environment before the rollout
    reaches it, and contact the owners of anything it returns:

    ```sql
    SELECT * FROM (
        SELECT 'tenant' AS scope, id, tenant_id AS target,
               usage_total_bytes, max_total_bytes, usage_object_count, max_object_count,
               usage_bytes_today, max_bytes_per_day, usage_objects_today, max_objects_per_day
          FROM quotas
        UNION ALL
        SELECT 'bucket', id, bucket_id,
               usage_total_bytes, max_total_bytes, usage_object_count, max_object_count,
               usage_bytes_today, max_bytes_per_day, usage_objects_today, max_objects_per_day
          FROM bucket_quotas
    ) q
     WHERE (max_total_bytes     > 0 AND usage_total_bytes   >= max_total_bytes)
        OR (max_object_count    > 0 AND usage_object_count  >= max_object_count)
        OR (max_bytes_per_day   > 0 AND usage_bytes_today   >= max_bytes_per_day)
        OR (max_objects_per_day > 0 AND usage_objects_today >= max_objects_per_day);
    ```

    Run it as a BYPASSRLS role — `quotas` is RLS'd.
  - Release note names the change explicitly.
- **Blockers:** none technical. Deliberately left as a human step: the rollout
  is safe on dev (where this landed) and needs a heads-up before it reaches an
  environment with real tenants.

### UI/UX refactor (2026-07-24): flatten the deep tenant→bucket→surface URLs

- **Status:** Deferred (the identity + navigation-legibility pass landed this
  session; the literal URL-depth cut is parked).
- **Landed this session (calm-product direction):** a single brand accent on a
  cool-slate ground (`globals.css` `.dark` token identity); the shell
  de-cluttered — `RealTimeStatus` collapsed from a four-chip strip (which cried
  red "ERR" for a merely-unknown rollup, in raw non-token colours) to one calm
  token-based status pill; Sidebar icons neutralised so only the active row
  carries the accent; the dashboard turned from a hybrid status-board-plus-
  launcher-grid into a pure "what needs attention" board (launcher grid + tile
  machinery removed, copy reframed); scope chrome de-duplicated to one control
  (top-bar `ScopePicker`) by dropping PageHeader's `ScopeBreadcrumb` pills and
  flipping `showDefaultActions` to default `false`; and `Breadcrumbs` rebuilt as
  the legible deep-route "you are here" anchor.
- **NOT done — why:** literally shortening
  `tenants/[id]/buckets/[backendId]/[bucketName]/{lifecycle,policy,versioning,
  object-lock,replication,object-keys}` (five segments). A bucket's identity is
  the composite `(tenant, backend, name)`, so a shallower URL (`/buckets/<id>/
  <surface>`) needs the backend to mint a stable single-token bucket id — a
  proto/schema change, not a frontend move. The existing UX already renders
  these as task-oriented **tab** screens (`TenantTabs` → `BucketTabs`), so the
  depth is in the address bar, not the click-path. It is also unverifiable in
  the lab today: `GetTenant` / `ListBuckets` return `fetch failed` at the
  BFF→backend hop, so `TenantLayout` shows its "Failed to load tenant" retry
  card and the bucket flow can't be exercised end-to-end.
- **Definition of Done:** either (a) accept the composite-keyed depth as
  correct and close this — the tab bars + breadcrumb already make it navigable —
  or (b) add a backend bucket id (UUID or opaque `<backend>:<name>` token) +
  resolver, collapse the route tree to `/buckets/<id>/<surface>`, and rewrite
  every `Link`/redirect plus `BucketTabs` and the `Breadcrumbs` `ENTITY_PARENTS`
  map, verified against a cluster where `GetTenant`/`ListBuckets` serve.
- **Blockers:** a backend bucket-identity decision; and a working backend for
  the bucket/tenant RPCs to verify the flow (the lab's ListBuckets/GetTenant are
  currently failing — an environment condition, not a UI regression).

### Remaining bucket sub-tab: Replication

- **Status:** Deferred (Versioning DONE; Replication blocked on the worker)
- **Reason:** The Versioning sub-tab now ships: `BucketService.SetVersioning`
  (role + Cedar + repo persist + Bucket.versioning on reads) was already real,
  and the frontend tab wires the enable + keep-deletes-forever switches to it
  (was a `BucketTabStub`). Replication stays a stub because the toggle would be
  inert without the data-mover behind it.
- **Definition of Done:**
  - Replication tab: blocked on a real replicator worker (see Features →
    "Replication: real `StorageReplicator` implementation"). The
    `BucketReplication` proto + `SetReplication` handler already exist, but
    enabling the toggle without a worker that actually copies objects would
    misrepresent the system state, so the tab stays stubbed until the worker
    lands.
- **Blockers:** the StorageReplicator worker.

### Phase 3: deprecate redundant resource-name shapes

- **Ratified under [ADR-0014](docs/adr/0014-canonical-resource-names.md)
  (2026-07-01).** In the plan's authoritative numbering this is **Phase 5**
  (soft-deprecate C on the wire) — gated on ≥1 week of real
  `paladin_resource_name_shape_total` data, which a deploy window does NOT supply.
  C is never dropped from the server (operator UI depends on it). Rest below
  kept for detail.
- **Status:** Open — gated on real-world shape-distribution data. The Phase-2
  central resolver is DONE: `internal/api/connectshim/resolve` exports
  `ResolveCollectionName(ctx, name) (CanonicalRef, error)` handling all three
  shapes (canonical A / tenant C / bare B; bare takes the tenant from ctx),
  and every collection call site goes through it.
- **2026-06-29 — metric is now actually exported.** The
  `paladin_resource_name_shape_total{shape}` counter was registered on the
  Prometheus *default registry*, which Paladin never serves (no `/metrics`
  handler, no Prom→OTLP bridge — Paladin exports via OTLP only). So it collected
  **zero observable data**. Migrated to the OTel meter
  (`metrics.RecordResourceNameShape`), so it now flows over the OTLP pipeline
  like every other Paladin metric. A deprecation decision is finally *possible*
  once traffic accrues.
- **Reason this remains:** the decision needs the real distribution. A local
  lab has no representative traffic (and runs `otel.enabled: false`), so the
  call cannot be made from dev data — dropping a shape clients actually send
  would break them. Also still open: whether the data plane's object-name
  parser (`connectshim/data/conv.go::objectNameParts`, a different
  `…/objects/{id}` shape that *also* does an `assertJWTTenant` cross-tenant
  guard) should move behind the resolver — a hot-path refactor with security
  semantics, hence left optional.
- **Definition of Done:** a deprecation decision per shape backed by ≥1 week of
  `paladin_resource_name_shape_total` data from a real deployment (`otel.enabled`);
  optionally fold the data-plane object-name parse into the resolver.
- **Blockers:** needs a real (non-lab) deployment emitting the metric over
  OTLP, then ≥1 week of traffic.
- **2026-06-30 — reviewed, still blocked.** Both halves were re-assessed:
  the shape-deprecation decision is unchanged (data-gated — no real traffic).
  The optional data-plane parser-fold was examined: `conv.go`'s
  `objectNameParts` parses ONLY the C-shape with an INLINE `assertJWTTenant`
  cross-tenant guard, whereas the central resolver accepts all three shapes
  and defers authz to handlers. Folding them is therefore a wire-contract +
  authz-placement change, not a mechanical dedupe — security-sensitive (cf.
  the cross-tenant `GetObjectKey` fix), so it stays optional/deferred rather
  than risk a regression for marginal dedup.

### `pg_cron` integration as alternative to in-process reapers

- **Status:** Won't-do (2026-06-30) — in-process reapers are the right model
  for Paladin; `pg_cron` is not an improvement here.
- **Reason:** Reviewed against Paladin's actual deployment. Paladin runs on **CNPG**,
  which does not ship `pg_cron` (confirmed: `pg_available_extensions` has no
  row) — enabling it needs a custom image + `shared_preload_libraries`, an
  infra dependency Paladin doesn't carry. The in-process reapers
  (`housekeeping.go`: RefreshTokenPurger, AuditLogPurger, IdempotencyKeyPurger,
  OperationsReaper) are partition-aware (DROP PARTITION on the partitioned
  tables), bounded-batch, observable via the app's OTel metrics/logs, unit +
  integration tested, and run in the worker's own process/credentials.
  `pg_cron` would split cleanup logic into raw SQL (away from Go and its
  tests), need separate observability (`cron.job_run_details`), and add a
  "worker exits when DB-side scheduling is on" mode — real complexity for a
  marginal "cheaper scheduling" win on an environment Paladin doesn't run. If a
  self-hosted-PG-with-pg_cron customer ever appears, revisit; until then the
  Go reapers are correct by design, not a stopgap.

### WAL archiving + PITR runbook

- **Status:** Deferred
- **Reason:** Audit log is the compliance-critical table. Any
  deploy beyond dev needs Point-In-Time Recovery configured at the
  Postgres layer.
- **Definition of Done:**
  - Documented `archive_command` setup (e.g. wal-g to S3).
  - Tested PITR restore drill — automated, not just a runbook.
  - RPO/RTO published in operator docs.
- **Blockers:** target Postgres deployment topology (RDS / Cloud SQL /
  self-hosted) — choice drives the archiving stack.

### Cross-region DB replication

- **Status:** Blocked
- **Reason:** Disaster-recovery posture for the Paladin DB itself. Not
  the same as `replication.enabled` on object storage.
- **Definition of Done:**
  - Streaming replica in a second region with documented failover
    procedure.
  - Latency target on the replica.
- **Blockers:** business RPO requirement (zero-data-loss vs minutes-
  scale lag) and budget for the second-region instance.

### API-test fixture for UI/UX with real-shape data

- **Status:** Mostly done — demo + load + stress flavours ship; only the
  `/billing` 24h time-series spread (API-infeasible) + a `task seed:up`
  port-forward wrapper remain.
- **Reason:** Most admin pages render gracefully when empty —
  `/events`, `/billing`, `/policies`, `/buckets`, `/objects`,
  `/audit`, `/capabilities` — but designing the populated state
  (truncation rules, pagination boundaries, status-pill
  combinatorics, time-series sparsity, error chips) requires
  realistic data sitting in front of the UI. Today the only ways
  to populate a tenant are (a) hand-clicking through every form,
  (b) writing one-off SQL inserts, or (c) running an end-to-end
  agent against a freshly bootstrapped cluster. None of those
  produce repeatable, scoped, easy-to-tear-down fixtures, so
  design / screenshot / demo work consistently lags the feature
  it's trying to evaluate.
- **State as of 2026-05-10:** Demo flavour landed —
  `backend/cmd/seed-fixture` (also reachable via
  `task seed:up` / `task seed:down`). Auth via bootstrap admin
  through the existing `internal/mcp.NewClients` client bundle.
  Seeds 4 EventSubscriptions on the caller's tenant covering
  the `/events` page states (HTTP baseline, NATS, disabled,
  CEL-filtered). RLS-aware: subs land on the caller's own
  tenant rather than minting a new fixture tenant, because the
  `event_subscriptions` policy enforces
  `tenant_id = paladin_session_tenant_id()` on WITH CHECK and
  cross-tenant create from platform.admin would fail there
  even though the handler-level guard passes.
- **Shipped (2026-07-01) — load + stress object seeding:** `--flavour=load`
  (default 500 objects) and `--flavour=stress` (default 1001 — just past a
  1000-row page) seed objects under an operator-supplied `--object-key` through
  the real UploadObject → PUT → CompleteObject flow (`seedObjects` /
  `uploadFixtureObject` in `cmd/seed-fixture`). Deterministic zero-padded user
  keys (`fixture/{flavour}/{NNNNNN}.txt`) so lexical == cursor order; idempotent
  (counts existing fixture objects, seeds the remainder); `down` hard-deletes
  them. `--count` overrides the default. So `/objects` listings + cursor
  pagination + the `object.*` event fan-out now have real data. Pure planners
  (`flavourObjectCount`, `fixtureObjectKey`, `isFixtureObjectKey`) are
  unit-tested.
- **Outstanding:**
  - `/billing` **time-series spread across 24h** — NOT reachable via the public
    API (it stamps `created_at = now()`; no backdating). Needs a server
    test-hook or a direct-SQL seeder — a separate concern from the API-driven
    fixture, deliberately out of scope here.
  - ~~First-run UX: a `task seed:up` port-forward wrapper.~~ **DONE
    (2026-07-02):** `task seed:up` / `seed:down` now port-forward the
    admin + api Services themselves, point the CLI at localhost with the
    new `--insecure-tls` flag (the planes present the cluster cert for a
    Service DNS name), and tear the forwards down on exit. Fixing this
    also surfaced + fixed a real CLI break: the idempotency middleware
    (RequireOnCreate) rejected every seed Create* — the CLI now stamps an
    Idempotency-Key per request like the frontend transport. Verified
    live: up seeds 4 subscriptions, down removes them, no leftover
    forwards.
- **Trigger to do load / stress:** the next time UI / UX work
  blocks on "I need to see this with real data" beyond what
  the 4 demo subscriptions cover — most likely `/billing`
  time-series, `/events` pagination + Last-test column,
  `/objects` listings.

---

## Architecture (post-review 2026-05)

### A tenant's own data-plane writes are not audited

- **Status:** Deferred — a decision, not an oversight.
- **Reason:** the data plane audits only a principal's calls inside a tenant
  other than its own (`middleware.AuditActingElsewhere`): a platform admin's
  work in a tenant it named. Uploads, deletes and batch calls by a tenant's
  own users and agents leave no audit row. Auditing them is a synchronous
  insert (ADR-0004) on the hot path of every agent upload, and a matching
  growth of `audit_log` and its partitions.
- **Definition of Done:** a decision on whether a tenant's data-plane writes
  belong in its audit trail; if so, the full audit interceptor on the data
  plane, with every read declaring `NO_SIDE_EFFECTS`, retention sized for the
  volume, and a test through the assembled data plane.
- **Blockers:** the throughput cost, which wants a measurement first.

### Audit form (B): staging table + projector (latency mitigation only)

- **Status:** Deferred — crash-durability is DONE via form (A)
  ([ADR-0004](docs/adr/0004-table-backed-audit-outbox.md)): the audit
  interceptor now writes synchronously to `audit_log` before the RPC
  returns, and `AsyncWriter` (the in-memory buffer that lost entries on
  `kill -9`) is removed. This entry is no longer a durability gap.
- **Reason:** form (A) puts one synchronous indexed append on the
  response path of every mutating RPC. If that tail latency ever shows up
  in the mutating-RPC p99 under load, form (B) keeps durability while
  moving the heavy `audit_log` write off the hot path: the handler appends
  to a lean `audit_outbox` table, a projector worker drains
  outbox → audit_log under SKIP LOCKED (same pattern as event_dispatcher).
- **Definition of Done:**
  - Migration adds `audit_outbox(entry_id UUID PK, payload JSONB,
    enqueued_at TIMESTAMPTZ)` with the same indexes as event_deliveries.
  - The interceptor writes to `audit_outbox` (ideally on the handler's own
    tx, via the ADR-0003 `RunInTx` seam) instead of `audit_log` directly.
  - A projector worker reuses the SKIP-LOCKED claim pattern and deletes the
    outbox row in the same tx as the `audit_log` insert.
- **Blockers:** none — gated purely on a measured p99 regression. Until
  then form (A) is correct and simpler.

### Per-tenant S3 bucket layout — Phase 3 (shared→dedicated copy job)

- **Status:** Phases 1 + 2 SHIPPED 2026-07-02/03 per
  [ADR-0015](docs/adr/0015-per-tenant-bucket-layout.md); **Phase 3
  slices 1 + 2 + 3 SHIPPED** (same-backend copy job + retention-gated cleanup
  live-verified end-to-end against Garage; cross-backend stream-through
  unit-tested — a live cross-backend run needs a second working backend, the
  dev `secondary`/SeaweedFS being off). Physical verify + frontend remain.
- **Shipped — Phase 2 (multi-backend routing):** `BackendRegistry`
  (client-per-backend, #121) → `backend_id` threading resolver→storage
  boundary (#122) → per-backend routers on `wire.Storage` (#123), proven
  end-to-end against two real MinIO backends (#124). Maintenance workers
  route by backend id (#125 bucket reconciler, #126 reconciler probe /
  hard-deleter / multipart reaper) and multipart uploads are anchored to
  their initiate-time (backend, bucket) (#127).
- **Shipped — Phase 1 (dedicated layout):** `tenants.storage_layout`
  (proto field 12, end-to-end #128); CreateTenant with
  `dedicated` provisions a pending tenant-owned bucket + default binding
  in the same tx, physically created by the (backend-routed) reconciler
  (#129); mutations are gated on `provision_state='ready'` with a clean
  retryable FailedPrecondition (#130). Provisioning now verifies the bucket
  is actually reachable (post-`CreateBucket` `HeadBucket`) before it can flip
  to `ready`, so a backend that accepts `CreateBucket` without yielding a
  writable bucket keeps the gate closed and surfaces `provision_error`
  instead of 500-ing every upload.
- **Shipped — Phase 3 slice 1 (same-backend copy job):**
  `tenant_storage_migrations` (resumable state machine + cursor); admin RPC
  `MigrateTenantStorageLayout` + `GetTenantStorageMigration` (provision the
  dedicated bucket + record the migration); `StorageMigrationWorker` (leased
  BackgroundJob) drives provisioning→copying (same-key server-side
  `CopyObject`, cursor-resumable)→transactional `object_keys` rebind + layout
  flip→verify→completed. Cross-backend pairs fail loudly (deferred to the
  stream-through slice). Source copies are RETAINED (cleanup is a later slice).
  Unit-tested state machine (happy path, bucket-wait, cross-backend fail).
- **Shipped — Phase 3 slice 2 (retention-gated cleanup):** adds
  `cleanup_retention_seconds` / `cleanup_after` / `cleaned_at` + a `cleaned`
  state. On completion the worker sets `cleanup_after = now() + retention`
  (default 24h, overridable via the RPC's `cleanup_retention_seconds`), and once
  the window elapses it deletes the old copies from the SOURCE bucket
  (keyset-scanned, `DeleteObject` per blob) and marks `cleaned`. Guard: cleanup
  never runs before `cleanup_after`. Unit-tested (delete-after-retention +
  waits-during-retention).
- **Shipped — Phase 3 slice 3 (cross-backend stream-through):** the s3
  `ObjectRouter.CopyObject` now handles cross-backend pairs — `Client.GetStream`
  (server-side GET → body reader) piped via `io.Copy` into the destination
  backend's multipart `Open` writer, so arbitrarily large objects copy without
  buffering. The migration worker's same-backend-only guard is removed; the
  router picks server-side copy vs stream-through transparently. Unit-tested
  (worker cross-backend proceeds).
- **Shipped — physical verify:** the verify phase now HEADs every object in the
  TARGET bucket and checks its size against the source's recorded `size_bytes`
  (size, not ETag — ETags differ between a server-side copy and a stream-through
  multipart upload); a miss / mismatch fails the migration instead of completing
  on a broken copy. Unit-tested (pass + size-mismatch fail).
- **Shipped — cost-attribution tagging:** the bucket reconciler now tags a
  dedicated (owned) bucket with `tenant_id=<uuid>` via `PutBucketTagging` right
  after `CreateBucket` (owner threaded through `ListPendingBucketProvisions` →
  `BucketProvisionRow.OwnerTenantID` → `BucketProvisioner.TagBucketOwner`).
  Best-effort — a backend without `PutBucketTagging` logs a warning and keeps
  the bucket usable.
- **Shipped — frontend migration status:** the tenant overview in the admin
  console shows a `StorageMigrationCard` (state badge + copied/total progress +
  source→target + error) that polls `GetTenantStorageMigration` while a
  migration is in flight and renders nothing when the tenant never migrated.
- **Shipped — Phase 3 cross-backend integration test:** a two-backend
  integration test (`tests/integration/components/storage_migration_crossbackend_test.go`)
  stands up two physically distinct MinIO backends and drives the router's
  cross-backend copy (`ObjectRouter.CopyObject` → `GetStream` piped into the
  destination's multipart writer) for a 12 MiB object, asserting size + a
  byte-for-byte sha256 match on the destination and that the source is retained.
  This closes the "cross-backend proven end-to-end" gap the unit tests couldn't.
- **Definition of Done (remaining — Phase 3 hardening):**
  - Live cross-backend run on the dev cluster (infra-dependent — needs the
    `secondary`/SeaweedFS backend back online; the integration test above is the
    code-side proof).
  - Public collections stay where they are: the schema refuses to rebind
    one (`collections_public_bucket_fixed`, ADR-0027), since its objects'
    URLs name the bucket. The job skips them, or refuses a tenant that has
    any before it starts, rather than failing halfway through the rebind.
- **Deferred (smaller follow-ups):** per-tenant backend selection at
  CreateTenant (currently the config default backend); org-prefix in the
  derived bucket name for cross-account global uniqueness; bucket tagging
  (`tenant_id`) at provision time for native cost attribution; frontend
  proto regen to surface `storage_layout` in the console.
- **Note — dev cluster object store is Garage (switched off SeaweedFS):**
  the `primary` backend now points at **Garage** (`garage-s3.storage:3900`,
  bucket `paladin-primary`, presigned via the `s3-garage.paladin.local` Traefik
  route owned by gitops's storage route bundle) — see `values-local.yaml`. We swapped off the co-located **SeaweedFS**
  (`seaweedfs-filer:8333`) because it maps each S3 bucket to a *collection*
  reserving its own volume(s); with the volume server at `-max=0`
  (auto-capped by disk → 18 volumes) ~10 buckets exhausted it
  (`No writable volumes and no free volumes left`) and **every** PUT (shared
  `paladin-1` included) 500-ed `InternalError` — an infra-capacity issue, not an
  Paladin defect. Garage has no per-bucket volume reservation; verified
  end-to-end (shared security-probe 6/6 + dedicated provision→upload→read).
  **Garage prerequisite:** the S3 access key needs the global create-bucket
  grant (`garage key allow --create-bucket <key>`) or the ADR-0015
  reconciler's `CreateBucket` is rejected. **Follow-ups:** (1) to move the
  change into the registry-pulled chart, `task deploy:backend`, then re-enable
  the Paladin ArgoCD app's `automated` sync (paused during the live cutover);
  (2) if a SeaweedFS-class backend is ever reused, consider a
  shared-bucket-with-prefix option since collection-per-tenant is costly
  there (real S3 / MinIO / Garage have no such reservation).
- **Blockers:** none technical — Phase 3 is an explicit admin action per
  tenant; build it when the first shared→dedicated migration is needed.

### Redis capability counter cache

- **Status:** Aspirational
- **Reason:** Capability tokens currently lean on Postgres for
  usage counters (calls / bytes per token) via the `UsageStore`.
  Under heavy presign traffic the row-lock per capability creates
  contention on a single hot row. A Redis INCR (with periodic
  flush back to Postgres via the outbox) collapses that into a
  ~µs op.
- **Definition of Done:**
  - Cache-aside `UsageStore` impl backed by Redis + periodic
    flush goroutine.
  - Bounded staleness contract documented (e.g. ≤ 5 s lag on the
    capability list page).
  - Feature flag to fall back to direct-Postgres if Redis is
    unreachable.
- **Blockers:** none.

## Capability module

### Background jobs still work on a trashed tenant

- **Status:** Deferred
- **Reason:** the trash freezes a tenant against API calls
  (`middleware.TenantFreeze`), but the workers do not consult it: the
  lifecycle worker expires its objects, replication copies them, the
  reconciler promotes its pending uploads, the dispatcher delivers its events.
  A restore then returns something other than what was trashed.
- **Definition of Done:** each worker that changes a tenant's data skips a
  tenant in the trash (or the decision to let one run is recorded per worker),
  with a test per worker.
- **Blockers:** whether event delivery for a trashed tenant should stop or
  drain.

### A user named without its tenant escapes the freeze

- **Status:** Deferred
- **Reason:** the IAM plane accepts a user by its short name, `users/{id}`,
  besides `tenants/{tenant}/users/{id}`. The freeze finds the tenant a call
  acts on in the names it carries, so a platform admin updating, deleting,
  granting scopes to or resetting the password of a trashed tenant's user by
  the short name is not refused.
- **Definition of Done:** the freeze resolves a user's tenant from a short
  name (or the short form is retired), with a test on each user RPC.
- **Blockers:** none.

### No retention for the tenant trash

- **Status:** Deferred
- **Reason:** a trashed tenant stays in the trash, frozen, until someone
  restores or purges it; nothing purges it after a period, and nothing says
  how long it has been there.
- **Definition of Done:** a configured retention after which a trashed tenant
  is surfaced for purge (or purged, once its data is gone), with the console
  showing the age of each trashed tenant.
- **Blockers:** a product decision on the period and on automatic purge.

### Check that PurgeTenant honours legal hold and COMPLIANCE retention

- **Status:** Deferred
- **Reason:** `PurgeTenant` refuses while the tenant owns collections, so
  locked objects are protected by the order of operations rather than by a
  check of their own. Nothing tests that purging cannot reach an object under
  legal hold or COMPLIANCE retention by any path.
- **Definition of Done:** a test that every path from a trashed tenant to its
  removal refuses while it holds a locked object, and a fix where one does
  not.
- **Blockers:** none.

### Capability module CI: deny network egress in the standalone job

- **Status:** Deferred — considered, not urgent.
- **Reason:** SC-006 claims the module suite runs with no network. The
  standalone job already satisfies the no-database and no-container halves (a
  bare runner), and — more importantly — the dependency-graph assertion already
  enforces the property that actually matters: no database driver, no storage
  SDK in the resolved graph. A network-egress firewall would add a weaker
  belt-and-braces guarantee (no test reaches out at runtime) at real flakiness
  risk on shared CI runners, where a firewall step interacts badly with module
  fetch and container networking. Not worth rushing for marginal assurance over
  a guard that already holds.
- **Definition of Done:**
  - A step denies egress (firewall or a network-restricted runner) AFTER
    `go mod download`, so dependency resolution still works, and BEFORE
    `go test`.
  - A deliberately network-touching test is shown to FAIL under it, so the
    control is proven rather than assumed.
- **Blockers:** none. A judgment call, currently made as "not yet".

### The MCP bridge cannot forward a key-bound capability

- **Status:** Deferred — follows from DPoP binding.
- **Reason:** the bridge forwards the capability it was handed to the plane
  behind it. A key-bound capability also needs a proof signed by the agent's
  key for *that* request, and the agent signs for the MCP request, not for the
  Connect call the bridge makes, so the plane refuses it. That fails closed;
  agents behind MCP use unbound capabilities until it is solved.
- **Definition of Done:** either the bridge verifies the agent's proof on the
  MCP request and the plane trusts a bridge attestation in its place, or the
  bridge holds its own key and receives capabilities delegated to it.
- **Blockers:** choosing between the two.

### Offline attenuation covers a fixed vocabulary, not arbitrary Datalog

- **Status:** Deferred — the vocabulary covers what Paladin enforces.
- **Reason:** a Biscuit holder narrows it with `paladin_op`,
  `paladin_resource_prefix` / `_uri`, `paladin_plane`, `paladin_expires`,
  `paladin_bind` and the copy limits `paladin_max_requests` /
  `paladin_max_budget_micros`, which the verifier folds into a Capability
  through `Narrows` and `Capability.Copies`. A block with Datalog rules or
  checks is refused rather than half-enforced: a check that reads `operation`
  or `resource` can only be evaluated where a handler names them
  (`AssertCapabilityOp`), and an RPC that never asserts would skip it
  silently.
- **Definition of Done:** Datalog checks evaluated by the Biscuit authorizer
  with `operation`, `resource`, `time` and `plane` facts on every capability
  RPC, with a gate proving that every RPC a capability reaches asserts its
  operation.
- **Blockers:** none. The every-RPC-asserts gate exists:
  `backend/tests/contract/capability_op_asserted_test.go` fails on a
  data-plane RPC whose handler does not call `AssertCapabilityOp`. Admin and
  iam are not gated — no capability carries a request there on its own —
  and `capability_planes_test.go` fails if that stops being true.

### The capability module's Go API still takes float64 amounts

- **Status:** Deferred — a breaking change to the module's public API.
- **Reason:** `Caveats.MaxBudgetAmount`, `ChargeRequest.Amount` and the
  usage types are float64. Storage is exact (`numeric(14,6)`) and every
  conversion rounds to the nearest micro (`AmountToMicros`), which is exact
  below `MaxMicros` (fifteen digits), so nothing drifts today; but the type
  invites float arithmetic in a consumer's own code. The token claim is a
  JSON number under the frozen wire format, so it cannot change on its own.
- **Definition of Done:** int64 micros in the module's API, with the token
  carrying the budget the same way in a new format version.
- **Blockers:** the token format version that offline attenuation also needs.

### No scanner sets object taint flags

- **Status:** Deferred — the flag, its API and its enforcement are in; the
  automation is not.
- **Reason:** objects carry `taint` signals (prompt injection, PII, secrets),
  `SetObjectTaint` sets and clears them under their own Cedar action, and a
  capability without `AllowTaintedRead` is refused reads of a flagged object.
  But only a person flags anything today, so content nobody looked at is
  readable by every agent.
- **Definition of Done:** a scanner runs on promote (and over existing objects
  once), sets the signals it detects through the same path `SetObjectTaint`
  writes, records which detector flagged what, and is tested against a corpus
  with known positives and negatives for each signal.
- **Blockers:** choosing the detectors, and their cost per object.

### The tag-definition handler is unreachable

- **Status:** Deferred.
- **Reason:** `backend/internal/api/data/v1/objecttagh` (create, get, update,
  delete and list of tenant tag definitions) is built into the wiring's
  repositories but no RPC constructs its `Handler`; the tag RPCs that exist
  work on an object's own tags through `objecth`. Its capability checks pass
  no resource, so if it is ever exposed a resource-restricted capability is
  refused all of it.
- **Definition of Done:** the package and its repository are removed, or an
  RPC exposes it with a resource form for tag definitions that
  `AssertCapabilityOp` can check.
- **Blockers:** deciding whether tenants need tag definitions at all.

---

## SDK

The contract-side half of ADR-0018. The client-side layers are in both SDKs.

### Python reads response headers through a transport of its own

- **Status:** Deferred.
- **Reason:** connect-python gives a client interceptor no response headers,
  so typed errors and `Retry-After` depend on `paladin.RelaySyncTransport` in
  the HTTP client; a caller who builds a generated client without
  `Client.http_client()` gets a warning and errors without `server_version`.
- **Definition of Done:** the interceptors read the headers from connect-python
  directly and `http_client=` leaves the README's generated-client example.
- **Blockers:** client-side response headers on `RequestContext`, or a public
  way to chain `ResponseMetadata`, in connect-python.

### An occasional abort at interpreter exit with the Python SDK loaded

- **Status:** Blocked (no reproduction).
- **Reason:** a consumer saw `recursive_mutex lock failed` abort a test process
  at exit in 3 of 28 local macOS runs with SDK 0.17. It has not reproduced:
  200 runs each on macOS of 0.17.0 alone, 0.17.0 with grpcio loaded as
  hatchet-sdk loads it, and 0.23 with mutual TLS, all exited 0. The message
  is libc++'s, which neither pyqwest (Rust) nor upb (C) links; grpcio, in the
  consumer's process through hatchet-sdk, does.
  `sdk/python/tests/test_lifecycle.py` now runs the scenario in fresh
  interpreters, a few times in every test run and 200 times in CI's
  `py-sdk-lifecycle` job.
- **Definition of Done:** a reproduction, its cause, and either a fix here
  (closing the SDK's transports deterministically at exit) or an upstream
  issue linked.
- **Blockers:** a reproducing run — from the consumer, with the process's
  native libraries listed, or from the lifecycle job.

## Tooling and observability

### `container_*` metrics do not exist on this cluster, and the scrape says otherwise

- **Status:** Blocked (OrbStack's kubelet, not our configuration).
- **Reason:** Alloy scrapes the node's cAdvisor endpoint and reports success —
  `up{job="cadvisor"} = 1` — while the endpoint returns 25 lines of
  `machine_*` and no `container_*` series at all. So there is no per-container
  CPU, memory or throttling data in VictoriaMetrics, and any panel or alert
  built on `container_cpu_*` would render empty while looking correctly
  configured. This was noticed while trying to establish whether a TLS
  handshake timeout came from CPU starvation in the api pod: the question
  could not be answered, because the data does not exist.
- **Definition of Done:** the documentation half is DONE —
  [docs/runbooks/no-container-metrics-on-orbstack.md](docs/runbooks/no-container-metrics-on-orbstack.md)
  records the symptom, the one-command confirmation, what still works, and
  what not to conclude from an empty graph. What remains is a working source
  of per-container metrics, and it is deliberately not being done here.
- **Blockers:** ownership, not difficulty. cAdvisor as a DaemonSet would fix
  it, but the Alloy install and the VictoriaMetrics stack are shared cluster
  infrastructure that Paladin neither owns nor deploys — its charts stop at its
  own namespace, and adding a DaemonSet to someone else's cluster to fix our
  view of it is not ours to do unilaterally. Raise it with whoever owns the
  observability stack. Nothing in the Alloy config needs changing for a real
  cluster: the same scrape returns full `container_*` data on a normal
  kubelet.
