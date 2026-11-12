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

### The Python SDK still supports Python 3.13

- **Status:** Blocked.
- **Reason:** only 3.14 and 3.15 should be supported, but `biscuit-python`
  0.4.0, which the `biscuit` extra (`paladin.attenuate`) needs, ships wheels
  up to CPython 3.13 only, and its pyo3 0.24 refuses to build for anything
  newer. Without 3.13 the extra installs nowhere and its tests run nowhere.
- **Definition of Done:** `requires-python = ">=3.14"`; 3.13 is gone from
  `sdk/python/compat.json`, `PY_SDK_PYTHON` in `.github/workflows/ci.yaml`
  (or the variable itself, the SDK job then following the runner), the
  `python_version < '3.14'` marker on `biscuit-python` in the `biscuit`
  extra and the dev group with `paladin.biscuit.WHEEL_PYTHON_BELOW`, and the
  README's compatibility table; `docs/upgrading.md` names the break; the
  attenuation tests run in CI on 3.14.
- **Blockers:** a `biscuit-python` release with a CPython 3.14 (or abi3)
  wheel.


### The SDKs are pre-1.0 and the Python SDK is not published (ADR-0043, step 7)

- **Status:** Blocked.
- **Reason:** limes is still at release candidates, so any SDK release can
  move consumers onto a breaking limes; there are no users outside the
  owner yet, so the Python SDK is installed from the repository. The
  distribution name `paladin-sdk` is provisional.
- **Definition of Done:** both SDKs cut 1.0 under semver; a publish step in
  `.github/workflows/release.yaml` sends the Python SDK to PyPI (trusted
  publishing, a `pypi` environment), its version coming from the
  `sdk/python/v*` tag through hatch-vcs.
- **Blockers:** limes 1.0; the decision to publish.

---

## Deploy cutover: rename to `paladin`

### The `paladin` name has not been checked against trademark registers

- **Status:** Open, not blocking.
- **Reason:** [ADR-0039](docs/adr/0039-the-name-is-qualified-where-it-is-published.md)
  keeps the name and qualifies every published artifact, which settles the
  registries but not trademark use. The USPTO search API needs a key, and
  the question wants a person qualified to answer it, not a lookup. It
  applies whether or not anything is published: a public repository is use.
- **Definition of Done:** someone qualified has answered whether "Paladin"
  for this software infringes a registered mark, and the answer is recorded
  in ADR-0039 — with a rename planned if it does.
- **Blockers:** none.

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

### Budget leases via MCP — intentionally excluded

- **Status:** Won't-do (by design) unless the issuance decision above is
  revisited.
- **Reason:** a lease pays off for an orchestrator whose sub-agents charge
  one budget in parallel; through the bridge an agent delegates nothing (the
  entry above), so it could lease only for itself, which a few calls per
  model step never queue behind. A lease holds the chain's budget until it
  is settled or expires, so an agent leasing too much starves its siblings
  in the tenant of budget meanwhile — a new way to hurt them, on the
  narrowest surface. `CapabilityService.Lease`, `GetLease` and
  `SettleLease` stay on the admin API, where an operator or an orchestrator
  presenting a capability with share calls them; `DefaultAlwaysDeny`'s
  `paladin_capability_*` already keeps them off the bridge.
- **Definition of Done (only if revisited):** the human-approved profile the
  entry above calls for, or an MCP client that runs sub-agents in parallel
  under one capability; then a `paladin_capability_lease` tool scoped to
  the caller's own capability and its descendants, with a cap on the amount
  and the TTL it may hold.
- **Blockers:** a product call; no current ask.

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
  - `internal/capability/kms` package with KMS-backed implementations
    of limes's `ContextSigner`, whose Sign call emits a Sign-API request
    instead of holding the key locally. limes signs through it as it
    does through a local key, so no signer of Paladin's own is needed.
  - SigningKeyPath stays as fallback for dev / on-prem deploys
    without KMS access.
- **Blockers:** none functional, but it's a compliance-driver feature;
  needs a customer ask before the KMS adapter implementations land.

### Role split: `scheduler` (extract cron-like triggers from worker)

- **Status:** Aspirational
- **Reason:** `worker` runs ALL background jobs today — 22 of them in
  `internal/app/build_jobs.go`: the reapers, purgers, reconcilers,
  partition maintenance, replication and lifecycle enforcement. Each
  holds its own `paladin.<Type>` lease (`cmd/server/serve_worker.go`),
  so one job cannot starve another of a lease; what they share is the
  pod's CPU and the reaper DB pool, and one resource limit covers every
  job class.
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
- **Trigger to do:** when one heavy job (replication) starts
  dominating the shared CPU or DB connection budget, OR when an
  operator reports a job missing its window because the pod was
  saturated.

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

### Roles build bundles they never use (ADR-0047 leftover)

- **Status:** Deferred.
- **Reason:** worker, dispatcher and ingest still read the API-token pepper
  and the DPoP nonce key, and iam every storage backend's credentials (with
  egress to storage), only because `BuildSharedDeps` builds those bundles in
  every role; their Roles therefore name Secrets their code never uses.
- **Definition of Done:** the bundles are built by the roles that verify API
  tokens or DPoP proofs alone, `sharedDepsSecrets` in
  `backend/internal/config/roles.go` loses both paths, the storage
  backends leave iam's set, and the per-role Roles and NetworkPolicies
  regenerated from them no longer name what each role does not use.
- **Blockers:** none.

### Console session terminated in Go (ADR-0048): removals

- **Status:** Deferred. The console calls every plane through the session
  role, which the chart deploys by default; what is left is removing what
  nothing uses now.
- **Reason:** the removals change chart values an overlay may still set, so
  they ship as their own breaking release.
- **Definition of Done:** the console chart drops `healthSnapshotTokenSecret`,
  `backend.healthRoles` and the role URLs only the BFF read, and its
  NetworkPolicy egress to the planes but iam (the OAuth consent relay); the
  backend chart's planes stop admitting the console's pods except iam.
- **Blockers:** none.

### Sign-in with an upstream code sends an empty subject

- **Status:** Deferred (found 2026-11-12 moving the console onto the session
  role, not caused by it).
- **Reason:** the login page's `?upstream_code=` path signs in with an empty
  subject, which `LoginRequest.subject` (`min_len = 1`) refuses, so the
  upstream-code sign-in cannot succeed.
- **Definition of Done:** either the proto accepts an upstream code without a
  subject and IAM resolves the subject from it, or the console drops the path;
  an e2e test signs in through it or proves it is gone.
- **Blockers:** whether any upstream IdP sign-in is still wanted (ADR on
  engineer-operated auth).

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

### Federated IdP via JWKS

- **Status:** Won't-do (for now) — not on the roadmap (2026-06-30).
- **Reason:** Paladin is engineer-operated and owns its own IAM (see the
  withdrawn *Phase 5b.1* above), so accepting tokens minted by an
  external IdP is **not needed now**. `auth.jwks_url` switches the planes
  (`internal/app/build_listeners_common.go`) and the MCP edge
  (`cmd/server/serve_mcp.go`) to `auth.NewJWKSVerifier`; ADR-0047 points it
  at Paladin's own IAM, not at an external IdP.
- **Reconsider only if:** a customer mandates SSO against their own IdP.
  The verifier reads the set and checks tokens through jwx, matching each
  token's `kid` against the active set; what remains is a rotation grace
  window, role-claim mapping such as `cognito:groups` → Paladin roles, and
  an end-to-end test issuing a token from a mock OAuth2 provider and
  verifying it through the data plane.

### Audit-log encryption at rest beyond filesystem-level

- **Status:** Deferred
- **Reason:** `audit_log.request_json`, `state_before_json` and
  `state_after_json` are BYTEA (migrations 094 and 099). They can carry
  policy diffs, secret refs, and PII-bearing tenant labels.
  Today they're protected only by Postgres filesystem-level encryption
  (whatever the deploy storage provides).
- **Definition of Done:**
  - Per-tenant data-encryption keys (DEK) wrapped by a KMS-managed
    key (KEK).
  - Audit middleware encrypts `request_json`, `state_before_json` and
    `state_after_json` with the tenant DEK before insert.
  - Read path decrypts on `ListAuditLog` / `ExportAuditLog`.
  - DEK rotation runbook.
- **Blockers:** KMS choice (AWS KMS vs Vault Transit vs cloud-agnostic
  envelope-encryption library).

---

## Performance / Scale

### Per-table autovacuum tuning

- **Status:** Blocked
- **Reason:** [the consolidated baseline](backend/migrations/001_initial_schema.sql) (was migration 008 before consolidation)
  set fillfactor on one table (`users`) but didn't touch
  `autovacuum_vacuum_scale_factor` / `autovacuum_analyze_scale_factor`
  per table — needs live bloat metrics to choose values that aren't
  guesses. A snapshot of `pg_stat_user_tables` on the local cluster
  (2026-11-02, ~17 h of uptime) shows why the local cluster cannot supply
  them: the only table with real churn is `worker_leases` (132k updates,
  all but three HOT, autovacuumed 729 times on 22 live rows — healthy at
  the defaults); every other table, `limes.usage` included, saw hundreds
  of writes. `pgstattuple` is available on the CNPG image but not
  installed.
- **Definition of Done:**
  - 30 days of `pgstattuple` / `pg_stat_user_tables` data.
  - Per-table override for the 5 hottest tables landing in a follow-up
    migration with measured before/after bloat.
- **Blockers:**
  - A representative workload: the local cluster runs smoke tests, not
    traffic.
  - A store for the 30 days: the local cluster runs the VictoriaMetrics and
    Grafana operators with no VMSingle and no Grafana instance, and Alloy
    forwards traces only, so no Postgres metric is kept anywhere.

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
      collection / quota / object / backend. Pattern in
      `internal/api/admin/v1/tenanth/handler.go::EventProducer`,
      reused across the sibling handlers; admin pod
      wiring in `build_listeners_admin.go`, api pod wiring
      in `build_listeners_api.go::apiEvents`. Event
      classes: `paladin.{tenant,bucket,collection,quota,object}.*`
      and `paladin.backend.credentials_rotated`.
    - **Cross-cutting classes** (opt-in): capability charge
      + audit-log mirror, both gated on cfg.Dispatcher
      toggles (`charge_events_enabled` /
      `audit_mirror_enabled`, default OFF). Adapters in
      `internal/app/{charge_emitter,audit_mirror}.go`
      bridge `*worker.EventRecorder` to
      `auth.ChargeEventEmitter` and
      `middleware.AuditMirrorEmitter`. Event classes:
      `paladin.capability.{charged,refunded}` and
      `paladin.audit.<action>`.
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
- **Trigger to do:** the first customer that flips charge /
  audit events on — validate the cardinality assumption holds
  for their tenant.

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
- **If a consumer genuinely needs push** rather than a query, the audit log
  is already streamed: `/audit/stream` on the admin plane
  (`internal/auditstream`, fed by migration 013). It routes by
  `actor_tenant_id`, so a platform-wide feed of purges is a change to that
  stream's audience, not a special case of tenant events, and should not be
  built as one.

---

## Database

### Index candidates considered and rejected (2026-08-18 audit)

- **Status:** Deferred — decisions recorded so the audit is not repeated from
  scratch. Pre-consolidation migrations 064–068 added the five indexes this pass judged worth
  their write cost; these are the ones it did not.
- **Reason (per candidate):**
  - **`objects (tenant_id, state) INCLUDE (size_bytes)`** — would turn the
    /stats object census and the quota reconciler's recompute into index-only
    scans. Not added: `objects_tenant_state_idx (tenant_id, state)` already
    makes the promote and delete UPDATEs non-HOT, so widening it would cost
    only index size — but it would speed a 15-minute background job and a
    dashboard poll, which no measurement has yet shown to be slow.
  - **`audit_log (at DESC, id DESC)`** — ListAuditEntries pages by that
    composite keyset while `idx_audit_log_at` covers only `at`. Rejected: ties
    on `at` are rare, audit_log is partitioned and already carries its
    primary key and five indexes, and every Paladin mutation writes a row —
    another index taxes the write path of the whole control plane for a
    tiebreak.
- **Definition of Done:** revisit each when there is production evidence —
  `pg_stat_statements` mean_exec_time, or `pg_stat_user_tables` seq_scan
  counts — rather than on inspection.
- **Blockers:** none; needs a real fleet to measure against.

### connectshim/iam: exhausted, with three equivalent mutants left

- **Status:** Deferred (the seam and the tests landed; this entry records why
  the score stops at 96%).
- **What landed:** an interface per handler-backed server — Auth, User,
  UserSettings; SystemServer has no handler — a table over the RPCs
  asserting a handler failure reaches the caller wrapping the original, plus
  cases for the parsing, forwarding and SUCCESS paths a failing double cannot
  reach. 78% after the seam alone; 97% now, over all 24 RPCs (2026-11-11).
- **Not a sample:** a budget of 300 produced only 92 mutations, so the
  operator set is exhausted. Every non-equivalent mutation in this package
  is caught.
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
  (operation `state` and `done`), 2026-10-06 (value sets), 2026-11-06
  (the bucket browser's collection list, tenant `labels` entries) and
  2026-11-07 (disjunctions across fields).
- **Reason:** The filterable list RPCs extract the SQL-expressible subset of
  the caller's CEL (`cel.ExtractPushdown`) and hand it to the query, so
  `filter` selects from the table rather than from whichever page the cursor
  landed on. The walk understands the top-level `&&` chain of string equality,
  `startsWith`, `contains`, booleans, and — as of 2026-08-28 — `created_at`
  ranges, threaded into all seven list queries. As of 2026-10-06 a string
  field's equality is a set of values, pushed as `= ANY(…)`: `field in [...]`
  and a disjunction of equalities on that one field reach the table too, in
  the six queries the generic walk serves (objects and audit keep their own
  walkers). As of 2026-11-07 the six split a filter into disjunctive normal
  form as objects do (`cel.ExtractPushdownBranches`), one query per branch
  merged in the list's order, up to eight branches. Strict `>` / `<` are widened
  to their inclusive forms deliberately: the pushdown may only narrow, so an
  extra boundary row is free and a missing one is a wrong answer.
  What still reaches only the in-memory pass: an arm of a disjunction that
  pushes nothing (which leaves the whole disjunction to it), negation,
  functions, and `updated_at` — which is left out on purpose rather than
  forgotten, since a mutable column pushed into the query can exclude a row
  that the CEL pass, running microseconds later against a row someone just
  touched, would have accepted. A filter made entirely of those reads the
  whole table one page at a time: correct (paging continues, no row is
  dropped) and slow.
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
request with tests and is deleted here by the one that closes it. What is
left waits on the owner: each entry says what was decided.

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

