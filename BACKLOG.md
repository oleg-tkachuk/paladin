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

## MCP bridge

### Live session enumeration on the streamable-HTTP transport

- **Status:** Deferred
- **Reason:** The new admin/v1.MCPInspectService surfaces profiles,
  always-deny, the tool catalog and upstream URLs (everything that's
  config-derived). Live session state — who is connected over
  streamable-HTTP right now, what session-id, last activity, total
  tool calls — lives inside `mcpsdk.Server` from
  `github.com/modelcontextprotocol/go-sdk` which doesn't expose an
  enumeration hook today.
- **Definition of Done:**
  - A new `MCPInspectService.ListSessions` RPC returns
    `[]Session{id, agent_subject, started_at, last_seen,
    tool_call_count, agent_type, model}` for the streamable-HTTP
    transport.
  - Implementation either upstreams a session-iterator hook to the
    SDK (cleanest) or wraps the SDK's `http.Handler` with a
    middleware that tracks session-id from the `X-Session-Id`
    header into a local in-memory map (simpler; no SDK fork).
  - `/mcp` UI swaps the placeholder card for a live table.
  - In the meantime, MCP tool calls remain visible via
    `/audit?audience=paladin-mcp` (deep-linked from the placeholder).
- **Blockers:** decide between SDK fork (clean but adds a maintenance
  burden) vs middleware wrapper (simpler, uses public SDK surface).

---

## Agentic plane / single-binary multi-mode migration

### Streaming RPCs through the inline transport

- **Status:** Deferred
- **Reason:** `internal/mcp/inline.go` routes Connect calls through
  `httptest.ResponseRecorder` — perfect for unary RPCs (which is all
  PALADIN exposes today) but the recorder buffers the full response before
  the round-trip returns. A streaming RPC would deadlock waiting for
  EOF that never comes until the handler also finishes reading the
  request body.
- **Definition of Done:**
  - Replace the recorder-based RoundTripper with an `io.Pipe` pair
    plus a goroutine running the handler concurrently, draining
    request body and producing a streamed response on the fly.
  - Smoke test: a server-streaming RPC (when one is added) returns
    chunks before the handler completes.
- **Blockers:** No streaming RPC in the current proto surface. Land
  the first one (likely a `WatchEvents` for the agentic event bus)
  before this becomes load-bearing.

### NetworkPolicies per role

- **Status:** Deferred
- **Reason:** Phase 4 landed per-role Deployments / Services / SAs but
  not NetworkPolicies. Today every pod can reach every other pod in
  the namespace; the role split is purely a process-isolation gain.
- **Definition of Done:**
  - Per-role `NetworkPolicy` keyed off `app.kubernetes.io/component`
    selectors. Default-deny ingress in the namespace, with explicit
    allow rules for ingress → api / mcp from ingress-controller pods,
    api → admin only from the worker (event dispatcher reasons), and
    api/admin/worker → Postgres + S3 egress.
  - Toggleable via `values.yaml` `networkPolicies.enabled` because
    not every operator runs a CNI that enforces them.
- **Blockers:** none. Pure chart work.

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

### Role split: `event-dispatcher` (extract webhook delivery from admin)

- **Status:** Deferred
- **Reason:** `worker.Dispatcher` lives in-process inside the admin
  pod today (mounted in `build_listeners_admin.go`). Webhook delivery
  is egress-heavy work with a fundamentally different failure mode
  from inbound admin RPC: a slow / flaky customer endpoint can hold
  HTTP connections open and starve the admin pod's connection pool.
  NetworkPolicy posture is also opposite — admin should have closed
  egress (Postgres + storage only), dispatcher needs egress=any:443.
  Splitting also lets you scale dispatchers independently when one
  tenant has 1000 webhook subscriptions to flaky endpoints without
  scaling admin.
- **Definition of Done:**
  - `cmd/server/serve_dispatcher.go` — new subcommand. Reads
    EventSubscription store, consumes undelivered events from an
    outbox table (or the existing in-memory dispatcher loop made
    durable), POSTs to sink, retries with exponential backoff,
    updates delivery status. Same role-port shape as worker (ops
    listener with /healthz + /readyz + /system/health.json).
  - Helm: `deployments.dispatcher.enabled: true` (default), single
    replica baseline, HPA-friendly (CPU + queue-depth metric when
    available).
  - NetworkPolicy: egress 0.0.0.0/0:443 + Postgres + storage; ingress
    only kube-proxy on /healthz.
  - admin pod stops mounting `worker.Dispatcher`; the existing
    `EventSubscriptionService.TestSubscription` RPC stays where it
    is (it's a synchronous one-shot that's fine on admin).
  - MCPInspectService gains a "dispatcher" component view (delivery
    queue depth, last error per subscription).
- **Trigger to do:** when webhook delivery latency starts impacting
  admin RPC p99, OR when one tenant's subscription failures begin
  starving the dispatcher loop in admin. Also worth doing
  pre-emptively before the first paying customer's webhooks land —
  avoid the on-call regret of "one subscription took down admin".

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

### Role split: `auth-server` (OAuth / OIDC isolation)

- **Status:** Aspirational
- **Reason:** When OAuth 2.0 authorization-code flow lands (per the
  OAuth BACKLOG entry below), the authorization server has its own
  attack surface (browser-facing /authorize, code storage, refresh
  rotation, JWKS rotation, client registration). Isolating from the
  iam plane lets you rotate signing keys without rolling api pods,
  apply a tighter NetworkPolicy on the OAuth endpoints, and run
  separate replicas for auth traffic vs request traffic.
- **Definition of Done:**
  - `serve auth-server` role hosting `/oauth/authorize`, `/token`,
    `/register`, `/.well-known/oauth-authorization-server`,
    `/.well-known/jwks.json`.
  - Independent JWKS rotation runbook.
  - api / admin verify tokens against the auth-server's JWKS — same
    code path that already exists for the federated-IdP case.
- **Trigger to do:** when OAuth lands (separate BACKLOG entry) and
  becomes the primary auth path for at least one customer. Until
  then, fold OAuth endpoints into the existing iam plane.

### Role split: `realtime` (SSE / WebSocket subscriptions)

- **Status:** Aspirational
- **Reason:** No realtime subscription feature today. When live
  audit-log streaming, capability-budget alerts, or tool-call
  spectator views land, they're long-lived connections with a
  totally different memory profile (one connection holds for
  minutes/hours vs ms-scale RPC). They don't belong on api pods
  that scale on request rate — pod restart latency would drop
  every active connection.
- **Definition of Done:**
  - `serve realtime` role with SSE / WebSocket handlers backed by a
    Postgres LISTEN/NOTIFY pump (or NATS / Redis pub/sub once
    that's around).
  - Per-tenant connection limits.
  - Graceful shutdown that drains existing connections instead of
    SIGKILLing them.
- **Trigger to do:** when the first real subscription feature
  ships. Skip until then.

### Phase 5b.1 — Drop user-authn IAM, accept OIDC

- **Status:** Blocked
- **Reason:** PALADIN currently mints HS256 JWTs against local `users` /
  `refresh_tokens` and exposes a login flow through
  `internal/api/iam/v1/auth_service`. The agentic-plane spec calls
  this an anti-feature for B2B integration — every buyer already
  runs Okta / Auth0 / Cognito / Keycloak. User authn is the
  customer's IdP problem, not PALADIN's.
- **Scope clarification (added after the M2M discussion):** this
  phase is about **user authentication only**. It does NOT touch
  `api_keys`, which serve a different purpose (machine-to-machine
  service tokens) and are modernised in Phase 5b.2 instead.
- **Definition of Done:**
  - `internal/auth` accepts JWKS-issued tokens from configured
    issuers; HS256 path retained only for `bootstrap.admin` first-run.
  - `users`, `refresh_tokens` tables removed via migration after a
    deprecation cycle; existing tenants migrated by a runbook.
  - `internal/api/iam/v1/auth_service`, `user_service`,
    `user_settings_service` deleted; the corresponding handlers,
    proto, and Connect mux registrations gone.
  - The `iam` listener stays for `api_key` operations until 5b.2
    runs; the chart still maps the iam port to the api role.
- **Blockers:** Customer IdP commitment (Auth0 / Cognito / Keycloak)
  + a migration plan for existing PALADIN-IAM users + a clear cutover
  signal (no live tenants on the local IAM path).


---

## Security

### RLS — coverage of cross-tenant tables

- **Status:** Open
- **Reason:** Migration 023 landed RLS on every directly-tenant-keyed
  table (`objects`, `object_tags`, `quotas`, `event_subscriptions`,
  `capability_records`, `api_tokens`, `multipart_uploads`,
  `multipart_parts`, `capability_usage`). `cfg.Security.EnableRLS=true`
  installs the BeforeAcquire / AfterRelease hooks on the pool that
  set `paladin.tenant_id` per acquisition.
  Out of scope for this slice (intentional, by table category):
    - `tenants`, `storage_backends`, `buckets`, `object_keys` —
      platform-admin reads cross-tenant.
    - `audit_log` — security/compliance reads cross-tenant.
    - `users`, `refresh_tokens`, `api_keys` (legacy iam) — login flow
      runs before tenant context is established.
    - `capability_revocations` — denylist must be visible to all
      tenants.
    - `operations` — workers consume across tenants.
- **Definition of Done:**
  - Decision per table on whether stricter policies are wanted (e.g.
    `audit_log` filter to writer's tenant on INSERT, free read on
    SELECT for compliance roles).
  - Integration tests that prove cross-tenant SELECT under
    `paladin_app` returns zero rows when GUC is set to a different
    tenant. (Today's test suite is unit-level; an integration
    harness against a real Postgres fixture is needed.)
- **Blockers:** none.

### Federated IdP via JWKS

- **Status:** Deferred
- **Reason:** `auth.jwks_url` is declared in config + types and there's
  a stub `auth.NewJWKSVerifier` constructor wired in `cmd/server/root.go`,
  but no live deploy uses it — every JWT today is HS256 minted by the
  IAM plane itself.
- **Definition of Done:**
  - JWKS verifier reads + caches keys from the configured URL with a
    rotation grace window.
  - `kid` claim required and matched against the active key set.
  - End-to-end test that issues a token signed by an external IdP
    (mock OAuth2 provider) and verifies through the data plane.
  - Documented role-claim mapping (e.g. `cognito:groups` → PALADIN roles).
- **Blockers:** which IdP(s) we commit to (Auth0 / Cognito / Keycloak).

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

### `audit_log` partitioning by `at`

- **Status:** Deferred
- **Reason:** Audit log is append-only with TTL-based DELETE. Bounded
  reapers (migration 008) cap WAL bursts, but vacuum still has to
  reclaim dead tuples. RANGE partitioning (daily or weekly) lets
  `DROP PARTITION` replace `DELETE` — zero dead tuples, zero VACUUM
  load.
- **Definition of Done:**
  - Daily/weekly partitions with attach/detach automation.
  - `housekeeping.audit_log_ttl` becomes "drop partitions older than
    this" instead of `DELETE WHERE at < cutoff`.
  - Migration plan that re-buckets existing data with bounded
    downtime.
- **Blockers:** insert rate metrics from a real deploy — pick interval
  granularity from observed daily volume.

### `idempotency_keys` partitioning by `expires_at`

- **Status:** Deferred
- **Reason:** Same shape as audit_log — insert + TTL-purge. Bloat
  scales with traffic.
- **Definition of Done:** mirrors audit_log partitioning above
  (hourly or daily depending on idempotency-key TTL distribution).
- **Blockers:** same as audit_log.

### AuditLog CEL pushdown — production benchmark

- **Status:** Deferred
- **Reason:** Pushdown extractor + dynamic-WHERE adapter shipped
  (`internal/filter/cel/auditpushdown.go`,
  `internal/store/postgres/adapters/admin_audit.go`). What's missing
  is the production-shaped benchmark proving the speedup is real:
  unit tests cover translation correctness but not Postgres latency
  on a representative dataset.
- **Definition of Done:**
  - Bench harness that seeds ≥1M audit rows in testcontainers PG.
  - Compare ListAuditLog with + without pushdown for a representative
    filter (action.startsWith + at >= range). Document ≥10× p50
    speedup or drop the claim.
- **Blockers:** none — bench plumbing only.

### Per-row Cedar filtering in ListObjects

- **Status:** Deferred
- **Reason:** `object.Handler.ListObjects` runs **one** Cedar check
  before the page fetch (objectKey-scoped). Per-row filtering would
  let policies decline individual objects (e.g. `tags['classified']
  == 'true'`), which v1 doesn't.
- **Definition of Done:**
  - Optional per-row mode triggered when the policy text references
    object-attribute predicates (parsed at compile time).
  - Skipped when the policy is a constant for the objectKey.
  - Pagination cursor stable across filtered/unfiltered modes.
- **Blockers:** policy-attribute analyzer in cedar-go.

### Per-table autovacuum tuning

- **Status:** Blocked
- **Reason:** [migration 008](migrations/008_db_optimization.sql)
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

### Replication: real `StorageReplicator` implementation

- **Status:** Aspirational
- **Reason:** [internal/worker/replication.go](internal/worker/replication.go)
  walks replicated buckets and logs intent; [cmd/server/root.go](cmd/server/root.go)
  injects `Replicator: nil` so the worker is dry-run only.
- **Definition of Done:**
  - `StorageReplicator` impl that performs cross-backend `CopyObject`
    via S3 SDK (AWS-native CRR for same-account, manual stream-copy
    otherwise).
  - Watermark advance + retry-with-exponential-backoff on transient
    errors.
  - Integration test using two MinIO instances.
- **Blockers:** scope decision — same-cloud only vs. cross-cloud.

### OAuth 2.0 authorization-code flow for MCP clients (Claude Desktop / Cursor / agent hosts)

- **Status:** Deferred
- **Reason:** Claude Desktop's "Custom Connectors" prefer OAuth
  authorization-code with PKCE for browser-based consent. PALADIN today
  authenticates MCP requests with either short-lived JWTs (15m, painful
  to refresh manually in a Connector form) or long-lived API tokens
  (paste-once, works fine but lacks browser consent UX). For now the
  documented Claude Desktop integration uses an API token minted via
  `APITokenService.Create`; OAuth is the proper-but-deferred path.
- **Definition of Done:**
  - **Discovery:** `GET /.well-known/oauth-protected-resource` (RFC
    9728) on the MCP plane pointing at the auth server, and
    `GET /.well-known/oauth-authorization-server` (RFC 8414) on the
    IAM plane listing supported response_types, grant_types, PKCE
    methods, scopes, token endpoint URL.
  - **Endpoints (RFC 6749 form-encoded, NOT Connect):**
    - `GET /oauth/authorize` — reuses existing IAM session (refresh
      cookie); when not logged in, redirects to `/login?next=...`.
      Renders a consent screen (frontend route) showing client +
      requested scopes; POST commits the decision and redirects with
      `code` to the registered `redirect_uri`.
    - `POST /oauth/token` — exchanges `code + code_verifier` for an
      `access_token` (re-using the existing `auth.JWTIssuer` with
      audience binding) and optionally a `refresh_token`. PKCE S256
      mandatory for public clients.
    - `POST /oauth/register` (RFC 7591) — dynamic client registration.
      Behind a feature flag; first cut can hardcode known clients
      (`claude-desktop`, `cursor`) in config.
  - **Storage:**
    - `oauth_clients` (id, redirect_uris[], allowed_scopes[],
      secret_hash NULL for public, created_at).
    - `oauth_authorization_codes` (code_hash PK, client_id, user_id,
      redirect_uri, code_challenge, scopes[], expires_at <60s,
      used_at NULL).
    - `oauth_refresh_tokens` — either new table or extend the
      existing `refresh_tokens` schema with an `oauth_client_id`
      column + audience tag.
    - All on `paladin_app` with RLS keyed on `user_id` / `tenant_id`.
  - **Consent UI:** new `/oauth/consent` page in
    `frontend/src/app/oauth/consent/page.tsx` rendering client name,
    scope list, Allow/Deny buttons; posts the decision back to the
    backend `/oauth/authorize` endpoint via the existing IAM
    transport. Localised same as login.
  - **Audience binding (RFC 8707):** access tokens carry `aud` set
    to the resource indicator the client requested (e.g.
    `mcp.paladin.local`); existing JWT verifier on the MCP plane
    enforces it. No data-plane token usable on admin and vice versa.
  - **Hardening:**
    - Codes single-use (mark `used_at` atomically; reject reuse).
    - Refresh tokens rotated on every grant (RFC 6749 §6).
    - `client_secret` (when present) hashed with argon2id, like
      api_tokens.
    - Rate-limit `/oauth/token` per client_id (re-use the
      api_token limiter).
    - CORS on `/oauth/token` for browser-based clients (preflight
      from Claude Desktop's renderer is acceptable).
  - **Config block** `oauth: { enabled: bool, dynamic_registration:
      bool, access_token_ttl, refresh_token_ttl,
      allowed_redirect_schemes: [https, claude-desktop, cursor] }`.
  - **Tests:**
    - Unit: PKCE verifier, code single-use, expiry, audience
      validation, refresh rotation.
    - Integration (hurl in `tests/api/e2e/oauth.hurl`): full
      authorise → token → refresh → revoke loop.
    - Manual: real Claude Desktop Custom Connector against
      `paladin.local` end-to-end.
  - **Cedar gating:** `oauth:authorize` action — admins control
    which roles can grant which scopes to which clients.
  - BACKLOG.md entry deleted in the same commit that lands the
    full Phase-1 slice.
- **Blockers:**
  - Decide whether to keep IAM as the auth server or fold it into a
    federated OIDC IdP (overlaps with the existing
    `Phase 5b.1 — Drop user-authn IAM, accept OIDC` entry above).
    Building OAuth here makes the OIDC migration cheaper because
    the discovery + endpoint shape is mostly the same; choose this
    deliberately, not by accident.
  - Consent-screen branding / scope-string copy — needs a product
    pass before exposing to non-internal Claude Desktop users.

### `ResetPassword` — email/SSO delivery

- **Status:** Deferred
- **Reason:** [userh/handler.go](internal/api/iam/v1/userh/handler.go)
  `ResetPassword` returns the new password to the caller (admin) so
  they can hand it off out-of-band. Self-service reset via email/SSO
  is the natural production model but isn't wired.
- **Definition of Done:**
  - Email-link reset flow with single-use signed token.
  - Integration with the federated IdP (depends on JWKS work above).
- **Blockers:** Email sender selection (SES / Sendgrid / SMTP).

### Operation execution: BatchCopy / BatchUpdateTags / BatchRestoreObjects

- **Status:** Aspirational
- **Reason:** Operations runner landed (`internal/worker/operations`)
  with atomic `ClaimNext` (`FOR UPDATE SKIP LOCKED`) and
  `BatchDeleteExecutor` wired in `BuildBackgroundJobs`. Calls to
  BatchCopy / BatchUpdateTags / BatchRestoreObjects now mark the
  operation row FAILED with `code=UNKNOWN_TYPE` instead of
  silently hanging — surfaces the gap to clients but the gap
  remains: no executor for those types.
- **Definition of Done:**
  - `BatchCopyExecutor` — needs storage client (S3 server-side
    copy), bucket lookup, statemachine for the destination row.
    Per-object error contract same as BatchDelete (partial success
    + failures list).
  - `BatchUpdateTagsExecutor` — repo.UpdateMetadata per object,
    merging the supplied tags map into the existing row.
  - `BatchRestoreExecutor` + connect handler unstub on
    `internal/api/connectshim/data/batch_server.go:77`. Currently
    returns `Unimplemented`.
  - Per-operation progress field on the metadata blob (e.g.
    `processed: N / total: M`) so polling clients can render a
    progress bar without waiting for terminal state.
  - Cancel path: when a polling client cancels, runner aborts
    mid-iteration cleanly (partial progress recorded).
- **Blockers:** none. Per-executor work; can land independently.

### Event dispatcher: NATS sink (recommended first non-HTTP sink)

- **Status:** Aspirational
- **Reason:** Cloud-native customers and most agentic-platform stacks
  (Letta / AutoGen / similar) already run NATS. Of the four broker
  options, NATS has the smallest dependency footprint
  (`nats-io/nats.go`, ~1MB), no SASL/SSL configuration ceremony, and
  CloudEvents-friendly subject conventions. Wiring NATS first
  validates that the outbox + dispatcher pattern handles non-HTTP
  sinks cleanly before we touch heavier brokers.
- **Definition of Done:**
  - Proto: extend `EventSink.oneof` with a `NatsSink` (URL, subject
    pattern, optional credentials reference).
  - `internal/worker/sink_nats.go` — connection-pooled client with
    reconnect / publish / health-ping.
  - `Dispatcher.deliverNATS` publishes a CloudEvents 1.0 envelope
    (`specversion=1.0`, `type=paladin.<resource>.<action>`,
    `source=paladin.local/...`, `data=<payload>`) to the configured
    subject. JSON encoding for the MVP.
  - Auth options: token, nkey, JWT/seed (all via SecretRef pattern;
    no inline credentials in YAML).
  - `cfg.Dispatcher.NATS.URL` (default empty = disabled), `MaxReconnect`,
    `ReconnectWait` knobs.
  - Health probe: dispatcher's `/system/health.json` gains a
    "nats:<server>" subsystem check that reports broker connectivity
    when at least one NATS sink is configured.
  - Frontend `/events` connector template: prefilled subject pattern
    + auth-field group for NATS sinks.
  - Tests: outbox row → NATS publish round-trip via embedded server,
    reconnect handling, envelope structure conformance.
- **Trigger to do:** real customer ask, OR launch of an
  agentic-platform integration that depends on NATS as its event
  bus. Don't pre-build before either signal.

### Event dispatcher: Kafka sink

- **Status:** Deferred
- **Reason:** [event_dispatcher.go:120](internal/worker/event_dispatcher.go)
  returns `"sink %q delivery not yet wired (slice 8)"` for `kafka`.
  Heavier dependency than NATS (~5MB for `franz-go` or
  `segmentio/kafka-go`) and brings configuration complexity
  (partition assignment, consumer groups, optional Schema Registry,
  SASL/SCRAM/mTLS auth). Worth doing only when a customer commits
  on Kafka — pre-built adapters tend to bake in choices the eventual
  customer will want changed.
- **Definition of Done:**
  - Kafka client library decision (`franz-go` preferred — pure-Go,
    actively maintained, no CGO).
  - Proto extension matches the existing `KafkaSink` stub
    (brokers, topic) plus credentials reference.
  - Per-tenant topic prefix (e.g. `paladin.{tenant_id}.{event_type}`)
    or operator-defined topic — pick after customer feedback.
  - CloudEvents envelope, same as NATS.
  - Sink-config schema validation at `Create`/`Update` time.
  - Tests: outbox row → Kafka publish round-trip via testcontainers
    or embedded redpanda.
- **Blockers:** customer ask + Kafka client library decision.

### Event dispatcher: RabbitMQ sink

- **Status:** Deferred
- **Reason:** Same proto stub returns `"not yet wired"` shape — but
  RabbitMQ isn't even in the proto today (no `RabbitMQSink` field in
  `EventSink.oneof`). Library footprint is moderate (~2MB for
  `rabbitmq/amqp091-go`), middling enterprise prevalence (legacy
  banking / fintech, slowly migrating off). Lower priority than NATS
  (cloud-native fit) and Kafka (enterprise standard); only worth
  building when a customer specifically runs it.
- **Definition of Done:**
  - Proto extension: add `RabbitMQSink` to `EventSink.oneof`
    (URL, exchange, routing_key, optional virtual_host, optional
    credentials reference).
  - `internal/worker/sink_rabbitmq.go` — connection-pooled client
    with `amqp091-go`, channel-per-publisher, reconnect on socket
    drop, publisher-confirms enabled (so `deliverRabbitMQ` only
    returns success after the broker ACKs the publish).
  - `Dispatcher.deliverRabbitMQ` publishes a CloudEvents 1.0
    envelope to the configured exchange + routing key. JSON body,
    `content_type: application/cloudevents+json`.
  - Auth: AMQP URL with embedded user:pass (resolved from SecretRef)
    or AMQPS with TLS client certs.
  - `cfg.Dispatcher.RabbitMQ.URL` (default empty = disabled),
    `MaxReconnect`, `Heartbeat` knobs.
  - Health probe: dispatcher's `/system/health.json` gains a
    "rabbitmq:<host>" subsystem check that reports broker
    connectivity when at least one RabbitMQ sink is configured.
  - Frontend `/events` connector template: prefilled exchange +
    routing-key fields + auth-field group for RabbitMQ sinks.
  - Tests: outbox row → RabbitMQ publish round-trip via
    testcontainers, reconnect handling, publisher-confirms behaviour
    when broker drops the channel mid-publish.
- **Trigger to do:** customer ask — typically banking / fintech
  enterprise that already has a RabbitMQ cluster as their event bus
  and won't migrate to NATS / Kafka for one new producer.

### NATS auth: NKey / JWT support

- **Status:** Deferred
- **Reason:** The two production NATS edges PALADIN cares about
  today both run unauthenticated:
    - **Outbound** — `Dispatcher` → NATS via `EventSubscription`
      `NatsSink`. The proto's `credentials_ref` accepts
      `<scheme>:<value>`; v1 only honours `token:<plaintext>`
      (see `internal/worker/sink_nats.go`).
    - **SeaweedFS publisher** — `gocdk_pub_sub` reads
      `NATS_SERVER_URL` from process env (set on
      `spec.filer.env` in `gitops/.../seaweedfs/seaweed.yaml`).
      No auth fields; gocloud.dev's natspubsub driver doesn't
      surface them.
  Fine for the lab cluster — NATS service has no exposed
  ingress and lives on the cluster network. Production needs
  one of NKey / JWT so a stolen Pod identity can't fan-out
  arbitrary events.
- **Definition of Done:**
  - `NatsSink.credentials_ref` supports `nkey:<seed>` and
    `jwt:<jwt>+nkey:<seed>` schemes. Need on-disk credential
    file materialisation (the nats.go client only accepts
    file paths for these). Use `os.CreateTemp` with
    `0600`-perm files, deleted on connection close.
  - `NATS_SERVER_URL` for SeaweedFS published via a
    Kubernetes `Secret` (the URL itself becomes
    `nats://<token>@host:port` for the simplest auth flavour).
  - `gitops` overlay enables NKey/JWT on the NATS broker
    deployment. Today `nats` chart runs auth-free.
  - Document the credential-rotation flow in `docs/`.
- **Trigger to do:** before any non-lab deployment of either
  the PALADIN NATS sink or the SF NATS publisher.

### Event dispatcher: SQS sink

- **Status:** Deferred
- **Reason:** Same proto stub returns `"not yet wired"` for `sqs`.
  Only matters for AWS-native customers. AWS SDK v2 already in tree
  (used by `internal/storage/s3.go`); adding `aws-sdk-go-v2/service/sqs`
  is light. Lower priority than NATS / Kafka because the customer
  base wanting SQS specifically is small (most AWS customers can use
  EventBridge or HTTP webhooks).
- **Definition of Done:**
  - `SqsSink` populated (queue_url, region, optional role_arn for
    cross-account delivery).
  - `deliverSQS` uses `SendMessageBatch` for throughput when the
    outbox poll returns multiple rows targeting the same queue.
  - IAM role wiring documented (when running outside EKS / not on
    AWS, expect explicit access keys via SecretRef).
  - CloudEvents envelope (encoded as the SQS message body string).
- **Trigger to do:** AWS-native customer with SQS as their bus.

### Event dispatcher: CloudEvents 1.0 envelope (cross-cutting)

- **Status:** Aspirational
- **Reason:** Outbound payload format is currently the raw `Event`
  struct. CloudEvents 1.0 is the CNCF-graduated standard for
  event-driven systems; consumers route / filter / dead-letter on
  attributes (`type`, `source`, `subject`) without parsing the
  body. The inbound ingest path
  (`internal/eventingest/source_cloudevents.go`) already speaks
  CloudEvents — outbound symmetry simplifies operator mental model.
- **Definition of Done:**
  - Decision: pick **JSON event format** (RFC 7159) for the wire,
    not Protobuf — broader consumer compatibility, easier debugging.
  - Outbound HTTP sink switches to `Content-Type:
    application/cloudevents+json`, body is the envelope.
  - NATS / Kafka / SQS sinks use the same envelope as the message
    body.
  - `type` follows `paladin.<resource>.<action>` convention
    (e.g. `paladin.object.uploaded`, `paladin.tenant.created`,
    `paladin.capability.revoked`).
  - `source` is the PALADIN deployment URL.
  - `subject` is the resource name when applicable.
  - `data` carries the existing `Event.Payload` map.
  - Existing webhook subscribers may break if they parsed the raw
    JSON shape — coordinate with operators or version the sink
    config (`v1` raw, `v2` cloudevents) for backward compat.
- **Trigger to do:** when the first non-HTTP sink lands (NATS most
  likely). Sink-side broker consumers expect CloudEvents — the
  format dichotomy "HTTP gets raw, NATS gets envelope" is the wrong
  thing to ship.

### Storage event ingest pipeline (SeaweedFS / MinIO → PALADIN ingest plane)

- **Status:** Deferred
- **Reason:** PALADIN has an `ingest` plane built (`serve ingest`,
  drivers `webhook | nats | rabbitmq` per `cfg.Ingest`,
  `internal/eventingest/`) but it's disabled in every overlay
  today. Its purpose: promote object rows from PENDING → AVAILABLE
  when storage notifies PALADIN that bytes landed. In the current
  production flow this is unnecessary — the agent path
  (`paladin_upload_object` → presigned PUT → `paladin_complete_object`)
  is explicit and synchronous, and the MCP tools / data plane RPCs
  already wire that loop end-to-end. Async ingest only matters
  when an external pipeline writes directly to the storage bucket,
  bypassing PALADIN, and PALADIN needs to discover those new objects via
  storage-side notification.
- **State as of 2026-05-10:** SeaweedFS filer publishes events
  to NATS via `gocdk_pub_sub` on subject `seaweedfs.filer`
  (image `chrislusf/seaweedfs:4.23_full`, notification.toml
  shipped via sibling ConfigMap mount, see
  `gitops/.../seaweedfs/seaweed.yaml`). NATS broker lives at
  `nats.nats.svc.cluster.local:4222`. End-to-end probe verified
  filer events arriving on the subject. **The publisher half is
  done; only the subscriber/decoder side on PALADIN is open.**
- **What's left:** PALADIN-side NATS subscriber that decodes SF's
  wire format (gocdk_pub_sub serialises body as
  `proto.Marshal(*filer_pb.EventNotification)` with metadata
  `{key: <fullpath>}`). The existing `SeaweedFSSource` parses
  the JSON shape SF's `[notification.webhook]` driver emits —
  not the protobuf one we receive on NATS. Two concrete
  sub-tasks:
    - **(a) New source adapter** `source_seaweedfs_nats.go`:
        - Decode NATS message body as `filer_pb.EventNotification`
          (vendor a minimal proto with just `OldEntry` /
          `NewEntry` presence — enough to derive
          create/update/delete; ignore Entry internals).
        - Read NATS header `key` (or gob-decoded metadata if
          gocloud.dev natspubsub falls back to gob — verify
          empirically via a test subscriber first).
        - Map (oldEntry, newEntry) presence → EventType:
          `nil → x = create`, `x → x = update`, `x → nil = delete`.
        - Use receipt time for `CloudEvent.Time` (TsNs is on
          `SubscribeMetadataResponse`, not on the inner
          `EventNotification` SF actually sends).
        - id = sha256(key + eventType + tsApprox)[:32].
    - **(b) Wire & deploy:**
        - In `gitops/.../paladin-values.yaml` set
          `ingest.enabled: true`, `ingest.driver: nats`,
          `ingest.nats.url: nats://nats.nats.svc.cluster.local:4222`,
          `ingest.nats.subject: seaweedfs.>`,
          `ingest.nats.queue_group: paladin-ingest` for idempotent
          horizontal scaling.
        - Decide JetStream vs core pubsub. Core pubsub is fine
          for v1; JetStream needs a stream pre-provisioned
          (out-of-band manifest in gitops).
- **Why this isn't urgent:** in the current production flow
  the agent path (`paladin_upload_object` → presigned PUT →
  `paladin_complete_object`) is explicit and synchronous; PALADIN
  doesn't need to discover writes async. The cycle only
  matters when an external pipeline writes directly to the
  storage bucket bypassing PALADIN — at which point the trigger
  fires. Until then the publisher half just sits there
  incurring zero cost (NATS core pubsub at-most-once with no
  subscriber drops messages on the floor).
- **MinIO equivalent:** if storage backend ever flips to
  MinIO, MinIO has cleaner native webhook + AMQP + Kafka
  bucket-notifications — an additional source adapter (mirror
  of SF's) and a `[bucket][notify]` config block on the MinIO
  side, then the same `ingest.driver=nats` wiring works.
- **Definition of Done:**
  - Pick a path based on storage backend in production AND
    customer requirement (do they write directly to S3 buckets
    bypassing PALADIN?). Document the choice in `docs/`.
  - Enable `cfg.Ingest.Enabled = true` per-overlay; deploy the
    `ingest` Helm role (already in chart, just `enabled: true`).
  - Configure storage-side notifications to point at PALADIN's ingest
    endpoint with a shared HMAC secret (matching
    `cfg.Ingest.Webhook.SharedSecret`).
  - Idempotency: `internal/eventingest/dedup_ttl` handles replays
    via a sliding-window cache; verify it's tuned for the storage
    notification retry profile (SeaweedFS retries aggressively on
    failed delivery).
  - Integration test: write an object directly via raw S3 API
    (bypassing PALADIN), wait for ingest worker to promote the row,
    assert object appears in PALADIN listing within Y seconds.
  - Handle the BYPASS_RLS race: ingest worker writes as
    `paladin_migrate` (cross-tenant) but the row's tenant_id has to
    come from somewhere — either the bucket prefix
    (`<tenant_id>/<object_key>/<key>` per current key shape) or
    the storage event payload. Confirm the parser handles
    malformed prefixes gracefully (orphaned objects in a
    `dead-letter` bucket).
- **Trigger to do:** real customer ask where their pipeline
  writes to the bucket without going through PALADIN RPCs. Until then,
  push them toward `paladin_upload_object`/`paladin_complete_object` —
  it's faster, more observable, and doesn't depend on the
  storage-notification subsystem's reliability. RabbitMQ-as-bus
  variant (Path D) only makes sense if the operator already
  invested in a RabbitMQ cluster AND has aversion to deploying
  Kafka/Redpanda — small intersection.

---

## Operational

### `pg_cron` integration as alternative to in-process reapers

- **Status:** Deferred
- **Reason:** Reapers in `internal/worker/housekeeping.go` are
  Go-loops by design (works on managed-PG without extensions). On
  self-hosted PG with `pg_cron` available, native scheduling is
  cheaper.
- **Definition of Done:**
  - Optional `housekeeping.engine: postgres-cron` mode that schedules
    the same purge SQL via `pg_cron`.
  - Worker exits cleanly when DB-side scheduling is enabled.
- **Blockers:** demand. Self-hosted PG with `pg_cron` extension is
  not the default PALADIN environment.

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
- **Reason:** Disaster-recovery posture for the PALADIN DB itself. Not
  the same as `replication.enabled` on object storage.
- **Definition of Done:**
  - Streaming replica in a second region with documented failover
    procedure.
  - Latency target on the replica.
- **Blockers:** business RPO requirement (zero-data-loss vs minutes-
  scale lag) and budget for the second-region instance.

### Per-worker observability runbooks

- **Status:** Deferred
- **Reason:** The five workers (reconciler, audit purger, refresh
  reaper, api-key expirer, lifecycle, replication) emit logs but no
  metrics or alert rules.
- **Definition of Done:**
  - OTel counter/gauge for each worker tick + outcome.
  - PromQL alert per worker for "stalled > 5× interval".
  - Runbook entry per alert.
- **Blockers:** observability stack choice.

---

## Documentation

_(no documentation items currently deferred)_
