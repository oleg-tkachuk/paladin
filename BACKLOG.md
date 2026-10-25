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
## SDKs

### The Python SDK holds protobuf-py below its latest release

- **Status:** Blocked.
- **Reason:** `protobuf-py` 0.7.0 moved the well-known type modules out of
  `protobuf.wkt`, and connectrpc 0.12.1 still imports them from there, so
  `sdk/python/pyproject.toml` requires `protobuf-py>=0.6.0,<0.7` against
  the rule that dependencies track their latest stable release.
- **Definition of Done:** a connectrpc release that imports under
  `protobuf-py` 0.7, taken here with the ceiling lifted to the next minor
  after the newest `protobuf-py`, and the wheel compatibility cells green.
- **Blockers:** connectrpc/connect-py#423.

### The Python SDK still supports Python 3.13

- **Status:** Blocked.
- **Reason:** only 3.14 and 3.15 should be supported, but `biscuit-python`
  0.4.0, which the `biscuit` extra (`paladin.attenuate`) needs, ships wheels
  up to CPython 3.13 only, and its pyo3 0.24 refuses to build for anything
  newer. Without 3.13 the extra installs nowhere and its tests run nowhere.
- **Definition of Done:** `requires-python = ">=3.14"`; 3.13 is gone from
  `sdk/python/compat.json`, `PY_SDK_PYTHON` in `.github/workflows/ci.yaml`
  (or the variable itself, the SDK job then following the runner), the dev
  group's `python_version < '3.14'` marker on `biscuit-python`, and the
  README's compatibility table; `docs/upgrading.md` names the break; the
  attenuation tests run in CI on 3.14.
- **Blockers:** a `biscuit-python` release with a CPython 3.14 (or abi3)
  wheel.

### The Python fake delegates wider than the server allows

- **Status:** Deferred.
- **Reason:** the Go fake (`paladintest`) refuses a delegation as the
  server's issuer does, through `limes.Narrows`, and cuts a child that would
  outlive its parent to the parent's end. The Python fake
  (`paladin.testing`) checks only the ops: it accepts a child with an
  audience, a resource or a start its parent lacks, and lets it outlive the
  parent. limes is Go only, so the Python fake needs the rule written out.
- **Definition of Done:** the Python fake refuses what `limes.Narrows`
  refuses — audience, window, tenant, key binding, resources, tainted-read,
  idempotency — and clamps the expiry, with the Go fake's cases in a shared
  table both fakes run.
- **Blockers:** none.

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

### Admin RPCs the MCP bridge leaves off on purpose

- **Status:** Won't-do (by design) unless the decision below is revisited.
- **Reason:** `DefaultCatalog` in `internal/mcp/profile.go` covers the
  backend, bucket and collection lifecycle and the subscription redrive;
  the writes are named outside `read_only`/`agent_safe` patterns, so only
  the admin profile offers them. These stay off the bridge:
  - `CreateBackend`, `UpdateBackend`, `RotateCredentials` — they point the
    platform at a storage endpoint and a secret reference, which an
    operator provisions.
  - `SetBucketPolicy`, `SetCollectionPolicy` and a `cedar_policy` on the
    bucket and collection writes — Cedar rewriting, refused for the same
    reason as `paladin_set_policy`; both tool names are in
    `DefaultAlwaysDeny`. (`paladin_create_collection` still takes one.)
  - Bucket `owner_tenant_id` changes and `public_read` — moving a bucket to
    another tenant or publishing it is a human call.
  - `SetObjectLock` (every version written under a COMPLIANCE default keeps
    a retention the database refuses to shorten) and
    `SetReplication` (no replicator applies it yet, see Replication below).
  - Tenant lifecycle and budget `Set`, as before; UserSettingsService, which
    holds console display preferences.
  - `OAuthClientService.DeleteOAuthClient` — it ends every agent's grant
    under that client, the bridge's own callers included.
- **Definition of Done (only if revisited):** a tool per RPC in
  `internal/mcp/lifecycle_tools.go`, its `DefaultCatalog` row, and the name
  dropped from `DefaultAlwaysDeny` where it is listed there.
- **Blockers:** a product call per item; no current ask.

### One bridge key bounds key-bound traffic through MCP

- **Status:** Deferred.
- **Reason:** the bridge signs every forwarded call with one key, and
  `capability/postgres.ReplayCache` keeps at most
  `dpop.DefaultReplayEntriesPerHolder` (1000) live proofs per key, each live
  for `dpop.DefaultWindow` (one minute). Past that the store refuses the
  proof, so key-bound calls through the bridge stop at roughly 1000 a minute
  across all its replicas. Unbound capabilities are not counted. No
  deployment is near it today.
- **Definition of Done:** the limit is lifted for the bridge without lifting
  it for every key — a larger share for the bridge's thumbprint, or a key
  per replica each published by `paladin_get_bridge_key` — with a test at
  the boundary.
- **Blockers:** none; wait for traffic that reaches it.

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
  `limes_charge_decisions_total` exist in the binary but no presign or
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
- **Reason:** No semantic-search feature today. When it lands, the
  embedder is fundamentally different
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
- **Reason:** The per-tenant billing dashboard has shipped: the console's
  `/billing` page reads `BillingService.GetTenantSummary` and
  `GetTenantTimeSeries` (`internal/api/admin/v1/billingh`), which aggregate
  the `charges` table on every request. That stays cheap until a tenant's
  charges per period grow large; then a dedicated job that pre-computes
  daily snapshots into a `billing_snapshots` table makes the dashboard
  render fast and isolates analytical-query load from OLTP.
- **Definition of Done:**
  - New `serve billing` role (or fold into `worker` as one more job
    if scheduler split happens first): periodic aggregation pass
    over `charges` → daily snapshot rows.
  - `BillingService.GetTenantSnapshot(tenant, period)` → fast read
    of pre-computed totals.
  - Snapshot retention policy + reaper.
- **Trigger to do:** when a real tenant's charges per period grow large
  enough (rule of thumb: ~1M rows) that the on-the-fly aggregation is
  slower than the SLA.

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
- **Trigger to do:** when OAuth becomes the primary auth path for at
  least one customer. Until then, the OAuth endpoints stay in the
  existing iam plane.

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

## Performance / Scale

### Folder listings read every key under the prefix

- **Status:** Deferred (correct at current scale; measure before changing).
- **Reason:** `list_object_prefixes` (migration 089) folds every key under the
  requested prefix to its first segment with a DISTINCT, so the folders at a
  collection's root cost a read of the whole collection. A skip-scan — jump
  from one folder to the next — needs an index that orders keys bytewise,
  and the only B-tree on `objects.path` is `UNIQUE (collection_id, path)` in
  the database's collation, while keys sort under `COLLATE "C"` everywhere
  else (085), so it cannot serve that walk.
- **Definition of Done:** measured at 1M and 10M objects in one collection
  (root folders, a deep prefix); if the root listing is too slow, an index on
  `(collection_id, path COLLATE "C")` and a recursive skip-scan in
  `list_object_prefixes`, with the same tenant gate and results, and the
  index's cost to writes measured too.
- **Blockers:** none — gated on a collection large enough to feel it.

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

### The compose stack's named SeaweedFS identity is unconfirmed end to end

- **Status:** Deferred
- **Reason:** `backend/deploy/seaweedfs-s3.json` now declares a named identity
  (`paladin-dev`, the same `dummy` keys `configs/compose.yaml` and
  `configs/local.yaml` sign with) and no `anonymous` one, so unsigned requests
  are refused unless a bucket policy opens them. Against that file on
  SeaweedFS 4.47 the feature probe reports every feature supported, including
  `ANONYMOUS_READ_POLICY`; with the old file it reported that one unsupported.
  Nothing in the dev stack reads the store unsigned: Paladin, the console,
  `frontend/scripts/dev-bootstrap.sh` and `scripts/live-smoke.sh` all go
  through signed or presigned requests. What has not run is the whole stack
  (`task stack:up`) on the pinned 4.48 image, as Docker Hub refused the pulls.
- **Definition of Done:** `task stack:up` comes up on this file, an upload and
  a download through the console or `scripts/live-smoke.sh` succeed, and the
  backend's probe reports `ANONYMOUS_READ_POLICY` supported; then delete this
  entry.
- **Blockers:** none.

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
  - Set `ReplicationTarget.Key` from the object row: the worker fills only
    the collection today, which is harmless while dry-run.
  - Integration test across two backends — the suites bring up two
    SeaweedFS instances for exactly this.
- **Blockers:** scope decision — same-cloud only vs. cross-cloud.

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

### Event dispatcher: configurable CloudEvents `source`

- **Status:** Deferred (owner decision, 2026-10-08).
- **Reason:** `source` is the fixed `cloudEventSource` (`paladin`) on every
  sink. Dedup does not need more: `id` is the delivery-row UUIDv7, so
  `source` + `id` stays unique across deployments, and `tenantid` already
  tells tenants apart.
- **Definition of Done:** an optional dispatcher config key (for example
  `config.dispatcher.event_source`) that defaults to `paladin`, validated as
  a URI-reference, stamped by `newCloudEventEnvelope`, with tests and the
  envelope table in `docs/event-delivery-dedup.md` updated.
- **Blockers:** a second Paladin deployment publishing into the same broker
  or webhook, where consumers need to know which one an event came from.

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

### Console UX programme (2026-10-19)

The entries below are one programme, agreed on 2026-10-19, to make the console
faster to work in and more finished to look at. Each lands as its own pull
request with tests and is deleted here by the one that closes it. Every entry
is `Aspirational` with no blocker unless it says otherwise; the order is the
order they are being taken up.

#### Dashboard: a trend for the tenants nearest their limits

- **Status:** Blocked.
- **Reason:** the dashboard lists the user's operations, the tenants near
  their budget (TenantBudgetService.Summarize), how many quotas sit at or
  near their caps and a first-run checklist, but not how any of them got
  there: nothing serves a usage or spend series across tenants.
- **Definition of Done:** a sparkline beside each tenant near its budget or
  quota, from a cross-tenant series the API serves, not assembled in the
  browser tenant by tenant.
- **Blockers:** the cross-tenant read limes is growing — see "Billing has no
  view across tenants".

#### Motion: view transitions and micro-animations

- **Status:** Deferred (owner decision 2026-10-19: animations are not wanted).
- **Reason:** pages swap without transition, and state changes happen in a
  single frame; the owner chose to leave it so.
- **Definition of Done:** View Transitions between pages and short
  transitions on the state changes that carry meaning, all off under
  `prefers-reduced-motion`.
- **Blockers:** none — taken up only if asked for.

#### CSV export of a list

- **Status:** Deferred (owner decision 2026-10-19: not now).
- **Reason:** a list cannot be taken out of the console.
- **Definition of Done:** CSV export of what a list shows, in its order and
  with its filters, from the rows the shared table hooks hold.
- **Blockers:** none — taken up when it is asked for.

#### Facets and pages for the remaining lists

- **Status:** Blocked.
- **Reason:** counted facets reach every list loaded whole that answers a
  question worth narrowing by (useTableFacets), and the operations page,
  which the server pages, through counts its listing gives with the first
  page (useServerFacets). What is left is paged on the server too — audit,
  collections, objects — and no List RPC of theirs counts yet, so a count
  beside them would describe only the rows read so far; the capabilities
  page browses by kind and subject through two server-side flags of its own;
  configuration is a document, not a list of rows.
- **Definition of Done:** audit, collections and objects filter through
  facets whose counts their List RPCs give, as ListOperations' facets and
  facet_counts do; or this entry names each one left and why.
- **Blockers:** facets and counts on ListAuditLog, ListCollections and
  ListObjects.

#### Facet choices remembered between visits

- **Status:** Deferred (needs an owner decision).
- **Reason:** Argo CD keeps a list's filters across visits. Here they live
  in the URL alone: a reload or a shared link keeps them, but a visit from
  the sidebar opens the list on its initial choice. Remembering them would
  open a list narrowed by a choice made weeks ago, which reads as rows gone
  missing, so it was left out until it is asked for.
- **Definition of Done:** if wanted, a table's choices saved per table in
  console.tables beside its page size, applied only when the URL names no
  facet, with the toolbar showing they are on and Clear filters clearing the
  saved ones too.
- **Blockers:** the owner's decision.

#### Topology: a tenant's policies and capabilities as a side branch

- **Status:** Deferred.
- **Reason:** the tenant topology draws tenant → collections → buckets →
  backends only; a policy layer is read one resource at a time
  (PolicyService.GetEffectivePolicy) and capabilities are listed one principal
  at a time (CapabilityService.List), so a branch for them costs a request per
  collection and per principal on every open.
- **Definition of Done:** the tenant topology shows which of its resources
  carry a Cedar layer of their own and the principals holding live
  capabilities, as a branch off the tenant, each linking to its page, from
  reads that answer for the whole tenant at once.
- **Blockers:** a tenant-wide read of policy layers and of capabilities.

#### Topology: collections in the platform view

- **Status:** Deferred.
- **Reason:** `/topology` draws backends → buckets → tenants; adding each
  tenant's collections means reading the cross-tenant collections catalogue,
  which on a large platform is thousands of nodes for a picture whose question
  is which tenants stand behind which infrastructure.
- **Definition of Done:** a platform topology can open one bucket or one
  tenant down to its collections (the tenant view already shows them per
  tenant), read on demand rather than all at once.
- **Blockers:** none — taken up when it is asked for.


#### Diff a policy against its earlier versions

- **Status:** Blocked.
- **Reason:** a Cedar policy is stored in place on its tenant, bucket or
  collection and no RPC returns an earlier text, so the policy editor diffs
  only the draft against what is saved. The audit log's before/after
  snapshots of a policy write are the nearest thing, one entry at a time.
- **Definition of Done:** the API serves a policy's previous versions, with
  who wrote each and when, and the policy editor diffs any two of them in
  `DiffView`.
- **Blockers:** the backend keeping a policy's history.

#### Diff the running configuration against its defaults

- **Status:** Blocked.
- **Reason:** `/config` diffs the running configuration against a
  config.yaml pasted in, but not against the defaults: GetConfig returns
  only the loaded YAML, and the defaults live in the Go config loader.
- **Definition of Done:** the API serves the defaults, redacted as the
  running configuration is, and `/config` diffs the two in `DiffView`, so
  an operator sees every setting their deployment changed.
- **Blockers:** a field on GetConfigResponse (or a sibling RPC) carrying
  the defaults.

#### Detail panels for the remaining record dialogs

- **Status:** Aspirational.
- **Reason:** `DetailPanel` took over the record dialogs of the audit log
  (both), capabilities, M2M tokens and operations. A collection's trash, the
  MCP sessions, tool catalogue and authorisation tables, and connected
  applications still open a dialog: their open row lives in component state,
  the MCP tables share one page and need namespaced parameters, and the
  trash's paged list may not hold a linked object after a reload, so it has
  to be read by id first.
- **Definition of Done:** each opens in `DetailPanel` through
  `useDetailPanel`, namespaced where a page holds more than one table, and a
  linked record the loaded page does not hold is read by its id.
- **Blockers:** none.
#### The tenants list cannot tell a tenant near its limits

- **Status:** Blocked.
- **Reason:** the tenants list's status bar, tiles and summary tell a tenant
  in the trash (Frozen) and one whose notice is a warning or critical from an
  active one, but not one near its quota or budget: ListTenants carries no
  usage, and reading each tenant's quotas from the browser is one call per
  row.
- **Definition of Done:** "Near quota" and "Over budget" states in
  `tenantStatus` (app/tenants/tenantView.ts), counted in the status bar and
  shown on the tiles, from usage the list read serves.
- **Blockers:** the cross-tenant read in "Dashboard: a trend for the tenants
  nearest their limits".

#### A bucket's notice is set through the API only

- **Status:** Deferred.
- **Reason:** a bucket shows the notice its `notice.paladin.io/*` labels
  carry, on its page and in every bucket list, but the console has no form
  that edits a bucket's labels; `UpdateBucket` with `labels` in the mask is
  the only way to set one. The tenant's edit dialog has the form.
- **Definition of Done:** the bucket's settings edit its notice as the
  tenant's edit dialog does, keeping its other labels as they are.
- **Blockers:** none. Storage backends carry no labels at all, so a notice
  on one would need a field in the API first.

