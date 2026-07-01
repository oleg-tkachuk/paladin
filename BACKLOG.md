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

### Tool-coverage gaps vs the PALADIN RPC surface

- **Status:** Deferred (partial — the data-plane cluster + admin read gaps
  landed 2026-06-26; 59 tools). Remaining items below need a product call.
- **Reason:** The MCP bridge (`internal/mcp/bridge.go`) exposes a curated
  subset of the ~110 PALADIN RPCs (59 tools). `DefaultCatalog` in
  `internal/mcp/profile.go` is the ground-truth list and is pinned to the
  real registrations by `TestServerRegistersDefaultCatalog`. Now wired:
  `UpdateObject`, `DeleteObjectTags`, `ListDistinctTags`, `BatchUpdateTags`,
  `RegenerateUploadUrl`, the 5 `MultipartUploadService` RPCs, data
  `CancelOperation`, admin `ResetUsage` / `GetAuditLogEntry` / `GetConfig`
  (`SystemService` client added). The agent-usable upload/tag mutations are
  in `agent_safe`; `ResetUsage` / `system_config` / batch tools stay
  admin-only. The following PALADIN capabilities are still **not** reachable
  from an MCP agent. Some are deliberate (see the next entry); the rest are
  unfilled coverage pending a product decision:
  - **Whole services with no client wired:** BillingService,
    TenantBudgetService, admin OperationService, UserSettingsService.
  - **Lifecycle writes** on Backend / Bucket / ObjectKey / Tenant
    (create/update/delete) — Tenant lifecycle is intentionally human-only
    (denylist); the others are gaps if agent-driven provisioning is wanted.
- **Definition of Done:** For each capability decided in-scope, add the tool
  in `registerReadTools`/`registerWriteTools`, append a `DefaultCatalog` row
  (the invariant test enforces this), and gate it into the right
  `DefaultProfiles` entry (destructive ops stay out of `agent_safe`). Wire any
  missing Connect client into `Clients`.
- **Blockers:** none technical. Needs a product call on whether an agent
  should drive provisioning (lifecycle writes) and billing/budget/settings
  surfaces vs. keep them human-operated.

### Capability / API-token issuance via MCP — intentionally excluded

- **Status:** Won't-do (by design) unless a human-gated profile is added.
- **Reason:** `DefaultAlwaysDeny` blocks `paladin_capability_*` and
  `paladin_apitoken_*`: an agent minting/delegating/revoking its own capability
  or M2M token is a trivial bypass of the caveat model the agentic plane is
  built on. The bridge forwards `X-PALADIN-Capability` but must never let the
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

### Phase 5b.1 — Drop user-authn IAM, accept OIDC — WITHDRAWN

- **Status:** Won't-do (for now) — direction reversed 2026-06-30.
- **Decision:** PALADIN is an **engineer-operated control plane** that
  integrates with other services and exposes an API for bucket access
  and management. Human authentication stays in PALADIN's **own IAM** (local
  `users` + HS256, and the OAuth Authorization Server in
  [ADR-0009](docs/adr/0009-oauth-authorization-server.md) = PALADIN IAM
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
- **Reason:** PALADIN is engineer-operated and owns its own IAM (see the
  withdrawn *Phase 5b.1* above), so accepting tokens minted by an
  external IdP is **not needed now**. `auth.jwks_url` + the stub
  `auth.NewJWKSVerifier` constructor (wired in `cmd/server/root.go`)
  stay inert and harmless — every JWT today is HS256 minted by the IAM
  plane itself.
- **Reconsider only if:** a customer mandates SSO against their own IdP.
  Then finish the verifier (cache + rotation grace window, required
  `kid` matched against the active key set, role-claim mapping such as
  `cognito:groups` → PALADIN roles) plus an end-to-end test issuing a token
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

### `ResetPassword` — self-service email delivery

- **Status:** Won't-do (2026-06-30) — out of scope by product direction.
- **Reason:** PALADIN is an **engineer-operated** service: its `users` are
  operators, not end-customers, so password resets are an operational task,
  not a self-service flow. An admin already calls
  [userh/handler.go](internal/api/iam/v1/userh/handler.go) `ResetPassword`,
  which returns the new password to hand off out-of-band — that is the
  intended model and it is sufficient. Same direction as withdrawing the
  external/federated IdP: human auth stays PALADIN's own IAM, kept deliberately
  minimal. Wiring an email-link self-service flow (and an email sender) would
  add surface area PALADIN's audience doesn't need.

### Event dispatcher: producer wiring — handler-class integration tests + adoption

- **Status:** Deferred (parent SHIPPED — only follow-ups remain)
- **State as of 2026-05-10:** Producer wiring complete across
  every handler class that today's customer surface needs:
    - **Lifecycle classes** (always-on): tenant / bucket /
      objectKey / quota / object. Pattern in
      `internal/api/v1/tenant/handler.go::EventProducer`,
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

### Event dispatcher: Kafka sink

- **Status:** Partially done — core sink SHIPPED 2026-06-30; auth/integration
  follow-ups remain.
- **Shipped:** `KafkaSink{brokers, topic}` (already in the proto) wired
  end-to-end — `internal/worker/sink_kafka.go` with `KafkaWriterPool` (one
  `segmentio/kafka-go` writer cached per (brokers, topic), `RequireAll` acks,
  `Hash` balancer); `deliverKafka` publishes the CloudEvents 1.0 envelope with
  the **tenant id as the message key** (per-tenant partition ordering); wired
  into `deliver()` + the delivery dispatcher; conv.go round-trip already
  mapped; unit tests (`sink_kafka_test.go`) via a `kafkaWriter` seam covering
  envelope/key, broker-list trimming, pool reuse, error + missing-config + nil
  -pool paths. Chose `segmentio/kafka-go` (pure-Go, no CGO) over franz-go.
- **Definition of Done (remaining):**
  - Auth: SASL/SCRAM + mTLS via `kafka.Writer.Transport` (today: PLAINTEXT /
    broker-list only). Credentials reference on the sink config.
  - Real-broker integration test (testcontainers redpanda / kafka): outbox
    row → publish round-trip. (Unit tests use a writer seam.)
  - Optional: per-tenant topic prefix vs operator-defined topic — operator
    -defined shipped; revisit if a customer needs auto-fan-out by tenant.
- **Blockers:** none — incremental; driven by a customer's auth posture.

### Event dispatcher: RabbitMQ sink

- **Status:** Partially done — core sink + health probe + UI form SHIPPED
  (through 2026-07-01); only the real-broker integration test + AMQPS
  client-certs remain.
- **Shipped:** `RabbitMqSink{url, exchange, routing_key}` added to the
  proto `EventSink.oneof` (field 5) + frontend types regenerated;
  `internal/worker/sink_rabbitmq.go` — `RabbitMQConnPool` (dial-per-URL,
  cached, redial on a dropped/closed connection), channel-per-publish with
  **publisher-confirms** (`deliverRabbitMQ` only returns success after the
  broker ACKs) and persistent delivery mode; wired into `deliver()` + the
  delivery dispatcher in `serve_dispatcher.go`; conv.go round-trip mapping;
  unit tests (`sink_rabbitmq_test.go`) via a `rabbitPublisher` seam covering
  envelope/routing, error mapping, and the redial-on-unhealthy path. Auth
  rides in the AMQP URL (`amqp(s)://user:pass@host/vhost`).
- **Shipped (2026-07-01) — health probe:** the dispatcher's
  `/system/health.json` gains a non-critical `rabbitmq` subsystem check
  (mirrors the `nats` one): `RabbitMQConnPool.Statuses()` reports each dialed
  broker's connection health, and a dropped/closed connection fails the probe
  (empty pool → healthy-but-empty). `preWarmRabbitMQ` scans rabbitmq-sink
  subscriptions at boot and dials each broker so the row is populated before
  the first delivery. `RabbitMQConnPool.Warmup` + `Statuses` unit-tested.
- **Shipped (2026-07-01) — UI connector form:** RabbitMQ is now a `SinkType`
  option in the `/events` subscription editor (it wasn't even selectable
  before). AMQP URL + exchange + routing-key fields wired to `RabbitMqSink`
  (`_form.ts` build/hydrate/validate + `SubscriptionEditorDialog`); the stale
  "Kafka/SQS are roadmap stubs" Target hint was corrected (all sinks are
  delivery-wired). `_form.test.ts` covers build + hydrate + validation.
- **Definition of Done (remaining):**
  - Real-broker integration test (testcontainers RabbitMQ): outbox row →
    publish round-trip + channel-drop-mid-publish behaviour. (The unit
    tests use a publisher seam, so wire compatibility is unproven.)
  - AMQPS with TLS **client certs** (today only URL-embedded creds /
    server-TLS via `amqps://`).
- **Trigger to do:** customer ask — banking / fintech enterprise already
  running a RabbitMQ cluster as their event bus.

### NATS auth: NKey / JWT support

- **Status:** Partially done — the PALADIN dispatcher half shipped
  2026-06-29; the broker + SeaweedFS-publisher + ops half remains.
- **Reason:** The outbound `Dispatcher` → NATS edge now supports NKey /
  JWT: `NatsSink.credentials_ref` honours `token:`, `nkey:<seed>` (in-memory
  `nats.Nkey` via `nkeys.FromSeed`), and `jwt:<jwt>+<seed>` (in-memory
  `nats.UserJWTAndSeed`) — no temp-file materialisation needed. Covered by
  embedded-server auth round-trip tests per scheme (nkey + decentralized
  JWT). What's still unauthenticated:
    - **SeaweedFS publisher** — `gocdk_pub_sub` reads `NATS_SERVER_URL`
      from process env (set on `spec.filer.env` in
      `gitops/.../seaweedfs/seaweed.yaml`). No auth fields; gocloud.dev's
      natspubsub driver doesn't surface them.
    - **The broker itself** — the `nats` chart runs auth-free, so the
      dispatcher's new NKey/JWT support has nothing to authenticate against
      yet in the lab.
  Fine for the lab cluster (NATS has no exposed ingress, cluster-network
  only). Production needs broker-side auth so a stolen Pod identity can't
  fan-out arbitrary events.
- **Definition of Done (remaining):**
  - `NATS_SERVER_URL` for SeaweedFS published via a Kubernetes `Secret`
    (the URL itself becomes `nats://<token>@host:port` for the simplest
    auth flavour).
  - `gitops` overlay enables NKey/JWT on the NATS broker deployment.
  - Document the credential-rotation flow in `docs/`.
- **Trigger to do:** before any non-lab deployment of the SF NATS
  publisher (the PALADIN sink half is ready now).

### Event dispatcher: SQS sink

- **Status:** Partially done — core sink SHIPPED 2026-06-30; throughput/ops
  follow-ups remain.
- **Shipped:** `SqsSink{queue_url, region}` (already in the proto) wired
  end-to-end — `internal/worker/sink_sqs.go` with `SQSClientPool` (one
  `aws-sdk-go-v2/service/sqs` client cached per region, lazy AWS-config
  resolution), `deliverSQS` publishes the CloudEvents 1.0 envelope as the
  SQS message body and sets `MessageGroupId`(tenant) + `MessageDeduplicationId`
  (stable delivery-row id) for **`.fifo`** queues; wired into `deliver()` +
  the delivery dispatcher; unit tests (`sink_sqs_test.go`) via an `sqsSender`
  seam covering standard + FIFO + error/missing-config paths. Auth = the
  default AWS credential chain (IRSA / env / shared config).
- **Definition of Done (remaining):**
  - Real-queue integration test (elasticmq / localstack testcontainer):
    outbox row → SendMessage round-trip. (Unit tests use a sender seam.)
  - `SendMessageBatch` when an outbox poll returns multiple rows targeting
    the same queue (today one SendMessage per row).
  - `role_arn` for cross-account delivery (today: ambient creds only).
  - Doc: IAM wiring for non-EKS / off-AWS (explicit keys via SecretRef).
- **Trigger to do:** AWS-native customer with SQS as their bus + a
  throughput profile that warrants batching.

### Event dispatcher: CloudEvents 1.0 envelope (cross-cutting)

- **Status:** Mostly done — UI selector SHIPPED (2026-07-01); only the
  default-flip decision remains (a breaking change, gated on operator
  coordination).
- **Shipped:** JSON-format CloudEvents 1.0 envelope (`newCloudEventEnvelope`
  in `sink_nats.go`) is the body of every broker sink — NATS, SQS, RabbitMQ,
  Kafka. `type` = `paladin.<resource>.<action>`, `source` = "paladin", `subject` =
  resource name, `data` = `Event.Payload`. The HTTP sink gained a `format`
  field (proto `HttpSink.format`): `""`/`"raw"` keeps the legacy raw Event
  JSON (Content-Type application/json) so existing webhook subscribers are
  unaffected, `"cloudevents"` sends the envelope with Content-Type
  application/cloudevents+json — the DoD's "version the sink config" backward
  -compat path. Unit-tested both HTTP formats (`sink_http_format_test.go`).
- **Shipped (2026-07-01) — UI selector:** the HTTP connector form gains a
  "Payload format" toggle (Raw JSON / CloudEvents 1.0) wired to
  `HttpSink.format` (`_form.ts` `HTTP_FORMAT_OPTIONS` + `buildSink` +
  `formFromSubscription` round-trip; `SubscriptionEditorDialog`). Unknown
  format strings fall back to raw. `_form.test.ts` covers the build + hydrate.
- **Definition of Done (remaining):**
  - Decide whether to flip the HTTP default `""` → `"cloudevents"` once it's
    confirmed no raw-shape webhook consumers remain (a breaking change, so
    gated on operator coordination).
- **Trigger to do:** before onboarding an HTTP subscriber that wants
  CloudEvents from the UI, or when retiring the legacy raw shape.

### Storage event ingest pipeline — JetStream upgrade + integration coverage

- **Status:** Deferred (parent concept SHIPPED — only follow-ups remain)
- **State as of 2026-05-10:** Full SF → NATS → PALADIN ingest pipeline
  works end-to-end on the local cluster, **including PROMOTE on
  a real PALADIN data-plane upload**. Verified live:
    1. `seed-fixture smoke-upload` → UploadObject creates a
       PENDING row, hands back a presigned PUT.
    2. PUT to the SF S3 gateway → 200, bytes land.
    3. SF fires filer event on `seaweedfs.filer`.
    4. Ingest pod's NATS subscriber decodes the gob+protobuf
       envelope (`source_seaweedfs_nats.go`), parses the path
       through the new `buckets/`-prefix-tolerant
       `parseSeaweedFSPath`.
    5. PromoteHandler.Lookup runs through a BYPASSRLS pool
       (mirroring dispatcher's pattern — same `MigrateDSN`
       wiring), finds the row, calls `PromoteToAvailable`.
    6. Row state flips PENDING → AVAILABLE.
  Ingest log line proves it: `"promote outcome … changed=true"`.
- **What's left (low priority, not blocking):**
  - **JetStream upgrade** — current binding is core pubsub
    (`jetstream: false`). Fine for the lab (missed events on
    a restart are caught by the data-plane Reconciler); prod
    deployments that need at-least-once should flip
    `jetstream: true` and pre-provision the stream
    out-of-band. Wiring already supports it (`runJetStream`
    branch in `driver_nats.go`); just needs broker-side
    setup + an overlay flag.
  - **MinIO source** — if storage backend ever flips to MinIO,
    MinIO has cleaner native webhook + AMQP + Kafka bucket-
    notifications. An additional source adapter (mirror of
    SF's) plus a `[bucket][notify]` config block on the MinIO
    side, and the same `ingest.driver=nats` wiring works.
  - ~~`buckets/` prefix observation — document the wire-format contract.~~
    **DONE (2026-06-29):** [`docs/storage-ingest.md`](docs/storage-ingest.md)
    documents the SF→NATS path contract (`<tenant>/<object_key>/<key>`), why
    the `buckets/` prefix is stripped (two publishers disagree on it), the
    drift risk, delivery semantics, and the MinIO path.
- **Trigger to act:** customer pipeline that writes directly
  to the storage bucket bypassing PALADIN RPCs (the entire
  raison d'être of the ingest plane), or production at-least-
  once requirement that needs JetStream.

---

## UI / Admin Console

### Migrate `middleware.ts` → `proxy.ts` (Next 16 convention)

- **Status:** Blocked (on a Next.js version that wires `proxy` into the
  standalone manifest).
- **Reason:** Next 16 renamed the edge-`middleware` convention to `proxy`;
  the build prints a deprecation warning for `src/middleware.ts`. We
  deliberately keep `middleware.ts` — on the pinned **16.2.6**, a `proxy.ts`
  compiles (`ƒ Proxy (Middleware)`) but takes the Node-runtime path
  (`runDependingOnPageType` → `onServer`, vs edge `onEdgeServer`) and is NOT
  written into `middleware-manifest.json`, so the standalone runtime never
  executes it — silently disabling the auth/CSRF route gate. The deprecation
  warning is cosmetic; `middleware.ts` still works (verified in the deployed
  stack). Switching now would be a silent security regression, so it's parked.
- **Definition of Done:**
  - On a future Next upgrade, rename `src/middleware.ts` → `src/proxy.ts` and
    `export function middleware` → `export function proxy` (config export
    unchanged).
  - Re-test the manifest: a `output: standalone` build must write the proxy
    function into `middleware-manifest.json` AND the standalone server must
    execute it (confirm the /login redirect + CSRF Origin gate still fire).
  - Delete this entry once the warning is gone and the gate is verified.
- **Blockers:** a Next.js release that fixes proxy→standalone-manifest wiring
  (recheck the 16.2.6 behaviour at the next upgrade — see the rationale block
  atop `frontend/src/middleware.ts`).

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

### Multi-segment ObjectKey: event-ingest path disambiguation

- **Status:** Mostly done (2026-06-30) — only the per-tenant cache remains.
- **Shipped:** longest-prefix disambiguation lands in the **shared promote
  handler** (`internal/eventingest/handler.go`) rather than per-source — every
  source funnels through `PromoteHandler.Handle`, so all four (seaweedfs,
  seaweedfs-nats, minio, cloudevents) get it for free. Handle recombines the
  source's naive `<object_key>/<key>` split, calls the new
  `ResolveObjectKeyPrefix` query (`object_keys.sql` — longest registered OK
  that prefixes the tail, `ORDER BY length DESC LIMIT 1`; object_key has no
  LIKE metachars so `|| '/%'` is safe), strips the resolved prefix for the
  real key, and threads the corrected OK/key into the lookup AND the emitted
  `paladin.object.uploaded` event. Covered by `handler_disambiguation_test.go`
  (Go glue) + `tests/integration/objectkey_prefix_test.go` (real-DB precedence
  incl. `invoices` vs `invoices/archive/2026`).
- **Definition of Done (remaining):**
  - Per-tenant prefix cache (invalidated on OK create/delete) so the resolve
    isn't a per-event query. object_keys is small per tenant, so the uncached
    query is acceptable for now — this is a throughput optimization.
- **Blockers:** none.

### Tenant slug min-length — NOT the object_key 2-char bug (misdiagnosis)

- **Status:** Resolved (2026-07-01) — decision recorded + DB drift fixed.
- **The 2-char question (Won't-do):** initially flagged as the twin of the
  object_key 2-char bug, but it is not. `ValidateTenantSlug`
  (`internal/api/v1/apiutil/slug.go`) has an EXPLICIT `len < 3` check and is
  documented as "3..63 chars, DNS-label-compatible"; migration 009 states the
  same intent. Tenant slugs double as Cedar `Tenant::"…"` UIDs and subdomain
  handles, so the 3-char floor is deliberate — 2-char slugs (`eu`/`hq`) are
  rejected ON PURPOSE. Relaxing it (as 045 did for object_key) would WEAKEN a
  deliberate constraint. Left as-is.
- **The 1-char DB drift (fixed in migration 046):** the migration-009 CHECK
  `slug ~ '^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$'` accepted a 1-char slug (the
  group is optional) while the Go validator requires ≥3 — a direct DB insert
  could create a slug the API rejects. Migration 046 adds
  `char_length(slug) BETWEEN 3 AND 63` so both layers enforce 3..63.
  `tests/integration/tenant_slug_format_test.go` pins it (1/2-char rejected,
  3/63 accepted).



### Phase 3: deprecate redundant resource-name shapes

- **Ratified under [ADR-0010](backend/docs/adr/0010-canonical-resource-names.md)
  (2026-07-01).** In the plan's authoritative numbering this is **Phase 5**
  (soft-deprecate C on the wire) — gated on ≥1 week of real
  `paladin_resource_name_shape_total` data, which a deploy window does NOT supply.
  C is never dropped from the server (operator UI depends on it). Rest below
  kept for detail.
- **Status:** Open — gated on real-world shape-distribution data. The Phase-2
  central resolver is DONE: `internal/api/connectshim/resolve` exports
  `ResolveObjectKeyName(ctx, name) (CanonicalRef, error)` handling all three
  shapes (canonical A / tenant C / bare B; bare takes the tenant from ctx)
  plus `ResolveTenantParent`, and every objectKey call site in
  `connectshim/admin/object_key_server.go` swapped to it.
- **2026-06-29 — metric is now actually exported.** The
  `paladin_resource_name_shape_total{shape}` counter was registered on the
  Prometheus *default registry*, which PALADIN never serves (no `/metrics`
  handler, no Prom→OTLP bridge — PALADIN exports via OTLP only). So it collected
  **zero observable data**. Migrated to the OTel meter
  (`metrics.RecordResourceNameShape`), so it now flows over the OTLP pipeline
  like every other PALADIN metric. A deprecation decision is finally *possible*
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
  `objectKeyNameParts` parses ONLY the C-shape with an INLINE `assertJWTTenant`
  cross-tenant guard, whereas the central resolver accepts all three shapes
  and defers authz to handlers. Folding them is therefore a wire-contract +
  authz-placement change, not a mechanical dedupe — security-sensitive (cf.
  the cross-tenant `GetObjectKey` fix), so it stays optional/deferred rather
  than risk a regression for marginal dedup.

### Phase 4: WhoAmI route table is capped, not paginated

- **Ratified under [ADR-0010](backend/docs/adr/0010-canonical-resource-names.md)
  (2026-07-01), plan Phase 4.** `AuthService.WhoAmI` now returns
  `repeated ObjectKeyRoute routes` — every ObjectKey the caller can read, in
  all three name shapes (A/C/B) plus (backend, bucket) — so clients normalize
  to canonical before sending (`internal/wire/objectkey_routes.go`).
- **Status:** Deferred (shipped with a hard cap; truncation is now EXPLICIT).
  The lister pulls `ListObjectKeys` in pages of `whoAmIRoutePageSize` (200) and
  stops at `whoAmIMaxRoutes` (1000). **Update (2026-07-01):** `WhoAmIResponse`
  gained `routes_truncated` (set true iff more readable ObjectKeys exist than
  the cap), so a client knows the table is an incomplete prefix and must fall
  back to `ListObjectKeys` — no longer *silently* truncated (DoD option b, the
  observable half). Full pagination of the route table (DoD option a) is still
  deferred — a page token on an identity RPC is awkward, and the cap is a safe
  default for tens–hundreds of ObjectKeys.
- **Reason:** WhoAmI's primary job is identity; an unbounded per-ObjectKey dump
  in the identity call is a response-size hazard for large tenants. The cap
  keeps the common case (tens–hundreds of ObjectKeys) correct and cheap. Route
  lookup is also best-effort: a failure (incl. a Cedar denial for a caller who
  can't list ObjectKeys) degrades to an empty table, never a WhoAmI 5xx.
- **Definition of Done:** either (a) paginate the route table (WhoAmI page
  token, or a dedicated `ListObjectKeyRoutes` RPC) so large tenants get a
  complete table, or (b) confirm from real usage that the 1000 cap is never
  hit and make the truncation explicit to clients (a `routes_truncated` flag).
- **Blockers:** needs real tenant-size distribution — same data gap as the
  shape-deprecation decision above. Until then the cap is a safe default.

### `pg_cron` integration as alternative to in-process reapers

- **Status:** Won't-do (2026-06-30) — in-process reapers are the right model
  for PALADIN; `pg_cron` is not an improvement here.
- **Reason:** Reviewed against PALADIN's actual deployment. PALADIN runs on **CNPG**,
  which does not ship `pg_cron` (confirmed: `pg_available_extensions` has no
  row) — enabling it needs a custom image + `shared_preload_libraries`, an
  infra dependency PALADIN doesn't carry. The in-process reapers
  (`housekeeping.go`: RefreshTokenPurger, AuditLogPurger, IdempotencyKeyPurger,
  OperationsReaper) are partition-aware (DROP PARTITION on the partitioned
  tables), bounded-batch, observable via the app's OTel metrics/logs, unit +
  integration tested, and run in the worker's own process/credentials.
  `pg_cron` would split cleanup logic into raw SQL (away from Go and its
  tests), need separate observability (`cron.job_run_details`), and add a
  "worker exits when DB-side scheduling is on" mode — real complexity for a
  marginal "cheaper scheduling" win on an environment PALADIN doesn't run. If a
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
- **Reason:** Disaster-recovery posture for the PALADIN DB itself. Not
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
  - First-run UX: today the CLI requires `--admin-url=` etc.
    when run from outside the cluster (DNS doesn't resolve
    cluster-internal Service names). A `task seed:up` wrapper
    that does port-forward → exec → cleanup would remove that
    friction; today operators hand-paste the localhost URLs.
- **Trigger to do load / stress:** the next time UI / UX work
  blocks on "I need to see this with real data" beyond what
  the 4 demo subscriptions cover — most likely `/billing`
  time-series, `/events` pagination + Last-test column,
  `/objects` listings.

---

## Architecture (post-review 2026-05)

### Cross-tenant scope switcher (ListMyMemberships + SwitchTenant)

- **Status:** Aspirational
- **Reason:** Today's UI ties an operator session to exactly one
  tenant — the `tenant` claim on their JWT (`frontend/src/
  context/ScopeContext.tsx:23–29` calls this out: *"the 1:1
  user-tenant model means switching tenants requires a different
  login"*). The `<ScopePicker>` shows the tenant as read-only
  metadata; backend + bucket are the only selectable axes.
  Platform-admin operators routinely need to inspect or operate
  on multiple tenants. Today they handle this by logging out and
  back in with a per-tenant credential, which is slow and burns
  audit-log noise per switch.
- **Definition of Done:**
  - New IAM RPC `ListMyMemberships() returns
    (repeated Membership { tenant_id, tenant_slug, roles[] })`
    — returns every tenant the caller's subject has a row in.
  - New IAM RPC `SwitchTenant(target_tenant_id) returns
    (TokenPair)` — mints a fresh access + refresh pair scoped
    to `target_tenant_id`. Authz: caller MUST be a member of
    the target tenant (i.e. ListMyMemberships includes it).
  - `<ScopePicker>` tenant row becomes interactive; selecting a
    different tenant calls SwitchTenant, swaps the cached
    tokens via the existing tokenStore, and triggers
    `AuthContext.refreshTenant()` so every downstream consumer
    sees the new scope.
  - Frontend Playwright e2e suite's US2 reverts to its
    original "Tenant scope switching" wording (the
    backend/bucket-scoped tests added in this branch remain
    as a complementary regression guard for the picker
    plumbing).
  - Audit-log Action recorded for every SwitchTenant call so
    operator session-scope changes are observable.
- **Blockers:** Cedar policy decision — does a SwitchTenant on
  an existing platform-admin require fresh consent for the
  target tenant, or is platform-admin transitive across all
  tenants? Settle before implementing; today's bootstrap
  bakes a per-tenant `platform.admin` row, which suggests
  per-tenant consent is the intent.

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

### Per-tenant S3 bucket layout

- **Status:** Deferred — explicitly held for product discussion
- **Reason:** Today every tenant lands inside one shared physical
  S3 bucket keyed by `tenants/<tenant_uuid>/buckets/<logical>/…`. A
  per-tenant *physical* bucket would simplify IAM blast radius
  (one AWS-side policy per tenant), unlock lifecycle / replication
  rules per customer, and remove the prefix-scan hotspot on the
  shared listing index. It would also complicate provisioning,
  multiply per-account bucket-quota pressure, fork the storage
  cost model, and require a backfill plan for existing data.
- **Definition of Done:** the user explicitly signals "go" — not
  before. When greenlit, design must cover: (1) provisioning flow
  (synchronous on CreateTenant vs async via outbox), (2) bucket
  naming + region pinning, (3) migration of existing tenants
  (rename in place via prefix copy, or treat legacy tenants as
  shared-bucket forever and only new tenants get their own bucket),
  (4) cost-attribution wiring (bucket name → tenant), (5) cleanup
  on PurgeTenant.
- **Blockers:** product decision. Do NOT start design without an
  explicit user request.

### paladin-worker split out of the monolith

- **Status:** Aspirational
- **Reason:** Today all planes — api, admin, iam, mcp, worker,
  ingest — run inside one Go binary, multiplexed by HTTP listener.
  The worker is the obvious first candidate to peel off: it owns
  the SKIP-LOCKED outbox loop, it scales orthogonally to RPC
  traffic, and a stuck dispatcher today can starve RPC handlers'
  goroutines in the same process. Splitting it gives independent
  rollout, independent HPA, and clearer ownership boundaries.
- **Definition of Done:**
  - New `cmd/worker` binary (or reuse `cmd/server --mode=worker`
    via the existing mode flag).
  - Helm chart adds a dedicated Deployment + ServiceAccount with
    only the outbox-write / event-publish IAM the worker needs
    (no public RPC roles).
  - Health probes wired to the dispatcher loop, not just /healthz.
  - The in-process worker shim in `cmd/server/serve_dispatcher.go`
    becomes opt-in for dev/minikube only.
- **Blockers:** none — but sequence after audit-outbox lands so we
  don't ship two competing dispatchers.

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

### NATS JetStream as the event bus

- **Status:** Aspirational
- **Reason:** Events today flow Postgres → outbox poller →
  per-subscription HTTP sink. Adding NATS JetStream between the
  outbox writer and the dispatcher gives durable fan-out, replay
  windows, and downstream consumers (analytics, search index)
  without further widening the SQL outbox table.
- **Definition of Done:**
  - JetStream stream provisioned via the chart.
  - Outbox writer publishes to JetStream subjects; current HTTP
    dispatcher becomes one consumer among others.
  - Replay tooling (rebuild a sink from sequence N).
- **Blockers:** operator preference (NATS vs Kafka vs Redpanda).

### SealedSecrets for prod-class clusters

- **Status:** Aspirational
- **Reason:** Minikube uses plain Kubernetes Secrets seeded from
  the helm values. A prod cluster needs the chart values
  (`auth.signingKeySecret`, `s3.adminCredentialsSecretRef`, etc.)
  encrypted-at-rest in git.
- **Definition of Done:**
  - SealedSecrets controller installed via gitops.
  - Chart switches to referencing pre-existing Secrets (already
    the contract today), and the SealedSecret YAML lives in
    gitops alongside the ApplicationSet.
  - Bootstrap docs walk through `kubeseal --raw`.
- **Blockers:** decision between SealedSecrets vs external-secrets
  with Vault.

---

## Architecture (post-review 2026-06)

_Context: full-codebase architecture audit on 2026-06-11 (backend,
frontend, infra). Items the audit surfaced that aren't already covered
elsewhere in this file. Handler-level tracing/metrics intentionally has
no entry here — it shipped as [ADR-0001](docs/adr/0001-otel-observability-baseline.md)
(traces + RED metrics + log↔trace correlation)._

## CI / Delivery pipeline

_Context: `.github/workflows/test.yml` + `security.yml` (added 2026-06-11)
mirror the lefthook gates (go vet / go test / buf lint / eslint / tsc,
gitleaks, trivy-fs). The items below are the deliberately deferred rest
of the pipeline._

### Playwright e2e suite wired into CI

- **Status:** Deferred
- **Reason:** the runtime sign-off is now closed (2026-06-30: suite is
  17/17 green, 170/170 under `--repeat-each=10` with zero flake, single
  run <10 s — well inside the SC-002 <3 min budget), so the suite has
  earned its flake budget and is safe to gate on. The remaining work is
  purely authoring the workflow file + flipping it to required.
- **Definition of Done:**
  - `.github/workflows/e2e.yml` builds both images, boots
    `tests/e2e/docker-compose.test.yaml`, runs `pnpm run test:e2e`,
    and uploads the Playwright report as an artifact on failure.
  - Workflow is required for merge alongside `test` / `security`.
- **Blockers:** none — sign-off complete. Note CI needs a reachable S3
  (the suite uses an external Garage via `PALADIN_E2E_S3_ACCESS_KEY/_SECRET_KEY`);
  the workflow must provision or point at one.


### Branch protection on `main` and `develop` — require status checks

- **Status:** Partially done — safe rules applied 2026-06-28; required
  status checks still deferred.
- **Reason:** The non-blocking half landed via the API on both branches:
  force-pushes disabled, branch deletion blocked, linear history required,
  `enforce_admins` on. The **required status checks** half
  (`backend`, `frontend`, `gitleaks`, `trivy-fs`) is intentionally still OFF
  because every Actions run currently fails at startup (0 steps executed) —
  the signature of an exhausted private-repo Actions minutes / spending
  limit. Requiring red checks would block all merges and direct pushes.
- **Definition of Done:** once Actions runs go green, add
  `required_status_checks` (strict) for the four check contexts. Use the
  dedicated sub-resource endpoint so the already-applied protections
  (force-push off, deletion off, linear history, enforce_admins) are
  preserved — a full `PUT …/protection` would clobber them:

  ```bash
  for br in main develop; do
    gh api -X PATCH "repos/oleg-tkachuk/paladin/branches/$br/protection/required_status_checks" \
      --input - <<'JSON'
  { "strict": true,
    "checks": [ {"context":"backend"}, {"context":"frontend"},
                {"context":"gitleaks"}, {"context":"trivy-fs"} ] }
  JSON
  done
  ```

  If the PATCH 404s ("required status checks not enabled"), the contexts
  have never been set on that branch — set them once via the full
  `PUT …/protection` (echo the current protection back in + add the
  `required_status_checks` block), then PATCH thereafter. Verified check
  contexts = the job ids: test.yml → `backend`, `frontend`; security.yml →
  `gitleaks`, `trivy-fs`, `trivy-image` (DoD covers the first four; add
  `trivy-image` only if image scans should gate too).
- **Blockers:** GitHub Actions billing — RE-VERIFIED 2026-06-30: every run
  still fails with `steps=0` and "The job was not started because recent
  account payments have failed or your spending limit needs to be
  increased." Account-level (Settings → Billing → spending limit / payment
  method); only the repo owner can resolve it. Enabling required checks
  before this is fixed would block ALL merges + direct pushes on both
  branches (enforce_admins is on), so it stays OFF until CI goes green.


---

## Documentation
