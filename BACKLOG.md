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
- **Guard in place:** `TestInlineRoundTripper_BuffersFullResponse`
  (internal/mcp/inline_test.go) executably pins the current buffering
  behaviour — a client sees no bytes until the handler fully returns.
  It fails the moment the recorder is swapped for the `io.Pipe` pair,
  which is the signal to write the real streaming smoke test above.
- **Blockers:** No streaming RPC in the current proto surface. Land
  the first one (likely a `WatchEvents` for the agentic event bus)
  before this becomes load-bearing.

### NetworkPolicies per role

- **Status:** SHIPPED (2026-07-02) — only enforcement verification on an
  NP-capable cluster remains.
- **Shipped:** `templates/networkpolicy.yaml` behind
  `networkPolicies.enabled` (default off). Default-deny ingress+egress
  scoped to the chart's own pods (`chart.selectorLabels`), then per-role
  allows keyed off `app.kubernetes.io/component`: ingress-controller/UI/mcp
  → api (8080/8085) + admin (8090), monitoring → ops ports, storage-ns →
  ingest webhook (8100); egress common (DNS, 443/6443 for the K8s API +
  https, Postgres, OTLP 4317) plus per-role storage/NATS, admin gets a
  configurable `adminBrokerPorts` list for the synchronous TestSubscription,
  and the dispatcher gets OPEN egress by design (customer sinks live on
  arbitrary endpoints — the blast-radius win of the split is that only it
  needs that). External namespaces/selectors are values-configurable.
- **Verified (2026-07-02):** enforcement semantics confirmed on kind+calico
  (the lab's orbstack CNI doesn't enforce NP): with the chart's rendered
  policies applied, an unlabeled pod → api:8080 is DENIED (default-deny
  ingress), a UI-labeled pod → api:8080 is ALLOWED (per-role allow),
  api-pod egress to an arbitrary intra-ns pod is DENIED (default-deny
  egress), and api-pod DNS egress works. Entry complete — delete on next
  touch if nothing new accrues.

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
- **Shipped — live audit feed:** migration 050 NOTIFYs on every `audit_log`
  insert; `internal/auditstream.Hub` (one LISTEN connection per admin pod,
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

- **Status:** Partially done — core sink SHIPPED 2026-06-30; SASL/SCRAM + TLS/
  mTLS auth SHIPPED 2026-07-01; only the real-broker integration test (+ the
  optional per-tenant topic prefix) remain.
- **Shipped:** `KafkaSink{brokers, topic}` (already in the proto) wired
  end-to-end — `internal/worker/sink_kafka.go` with `KafkaWriterPool` (one
  `segmentio/kafka-go` writer cached per (brokers, topic), `RequireAll` acks,
  `Hash` balancer); `deliverKafka` publishes the CloudEvents 1.0 envelope with
  the **tenant id as the message key** (per-tenant partition ordering); wired
  into `deliver()` + the delivery dispatcher; conv.go round-trip already
  mapped; unit tests (`sink_kafka_test.go`) via a `kafkaWriter` seam covering
  envelope/key, broker-list trimming, pool reuse, error + missing-config + nil
  -pool paths. Chose `segmentio/kafka-go` (pure-Go, no CGO) over franz-go.
- **Shipped (2026-07-01) — SASL/SCRAM + TLS/mTLS auth:** `KafkaSink` gains
  `sasl_mechanism` ("" | plain | scram-sha-256 | scram-sha-512),
  `sasl_username`, `sasl_password`, `tls_enabled`, and `tls_client_cert` /
  `tls_client_key` (PEM, mTLS). `buildKafkaTransport` maps the config to a
  `kafka.Transport{SASL, TLS}` (nil = plaintext); the writer pool now keys by
  `(brokers, topic, auth-hash)` so distinct-credential sinks never share a
  writer; unsupported mechanism / bad mTLS keypair fail the delivery with a
  clear error. Admin form exposes the SASL mechanism + username/password + a
  TLS toggle; mTLS client-cert/key stay API/config-only (PEM key material in a
  browser form is a security smell). Unit-tested transport construction (SASL
  mechanisms, TLS, mTLS keypair incl. a generated cert, error paths) + the
  cache-key uniqueness + delivery threading. Inline creds are lab-grade — a
  secret-store-resolved ref is the remaining hardening (shared with NATS).
- **Shipped (2026-07-02) — broker verify + secret-store creds:**
  `TestKafkaSinkDelivery_SCRAM` (internal/integration, tags=integration) runs
  DeliverOne against a real redpanda with SASL/SCRAM-SHA-256 + authorization
  enabled — handshake, per-tenant message key, and CloudEvents envelope all
  consumed back; passed locally against Docker. Credential fields
  (`sasl_username/password`, `tls_client_cert/key`) now accept
  `k8s:<name>/<key>` Secret refs resolved at delivery time
  (worker/sink_secrets.go; TTL-cached, pool key hashes the RESOLVED material
  so rotation dials a fresh writer).
- **Shipped (2026-07-02) — SASL_SSL vs certs-mounted broker + private CA:**
  `KafkaSink.tls_ca_cert` (PEM bundle; also accepts a k8s: Secret ref)
  verifies brokers behind a private CA — setting it implies TLS and keys the
  writer pool. `TestKafkaSinkDelivery_SASL_SSL` runs DeliverOne over
  SCRAM-SHA-256 + TLS against a redpanda mounted with a generated server
  cert, verified via tls_ca_cert; PASSED locally against Docker.
- **Shipped (2026-07-02) — mTLS vs require_client_auth broker:**
  `TestKafkaSinkDelivery_MTLSRequireClientAuth` hand-rolls the redpanda
  module's two-phase config trick (the module's embedded template can't
  express client-auth) to run a broker with `require_client_auth: true` +
  a truststore: the sink delivers with `tls_client_cert/key`, and the same
  transport WITHOUT the keypair is refused at the handshake (negative
  pinned). PASSED locally against Docker.
- **Definition of Done (remaining):**
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
- **Shipped (2026-07-02) — broker verify + secret URL:**
  `TestRabbitMQSinkDelivery` (internal/integration, tags=integration) runs
  DeliverOne against a real rabbitmq:4.0 — publisher-confirmed publish
  consumed back as the CloudEvents envelope; passed locally against Docker.
  The AMQP URL (credentials embedded) now accepts a `k8s:<name>/<key>`
  Secret ref resolved at delivery time.
- **Shipped (2026-07-02) — AMQPS client certs:** `RabbitMqSink` gains
  `tls_client_cert` / `tls_client_key` / `tls_ca_cert` (PEM; each also
  accepts a k8s: Secret ref). `buildRabbitTLS` mirrors the Kafka transport
  build; the conn pool now keys by URL + TLS material so same-URL sinks
  with different certs never share a connection (Warmup covers URL-auth
  sinks only — client-cert sinks dial lazily).
  `TestRabbitMQSinkDelivery_AMQPSClientCert` runs DeliverOne against a
  RabbitMQ whose TLS listener REQUIRES a client cert (verify_peer +
  fail_if_no_peer_cert), including the negative (no client cert → handshake
  refused); PASSED locally against Docker.
- **Shipped (2026-07-02) — connection-drop recovery under the real
  broker:** `TestRabbitMQSinkDelivery_ConnectionDropRedial` delivers, has
  the broker force-close every AMQP connection (`rabbitmqctl
  close_all_connections`), then proves deliveries either fail loudly
  (retryable) or succeed after the pool's health-check redial — and drains
  the queue to confirm every reported success actually landed (publisher
  confirms → no silent losses). Entry complete — delete on next touch if
  nothing new accrues.
- **Trigger to do:** customer ask — banking / fintech enterprise already
  running a RabbitMQ cluster as their event bus.

### NATS auth: NKey / JWT support — broker-side (gitops)

- **Status:** Partially done — the PALADIN-side (client) half shipped 2026-06-29.
  The remaining work is **broker-side and lives in gitops** (separate repo),
  not here — there is nothing left to implement in this repo. The
  SeaweedFS-publisher leg of the original DoD is now **moot** (Garage — see
  below).
- **Shipped (this repo):** the outbound `Dispatcher` → NATS sink supports NKey /
  JWT. `NatsSink.credentials_ref` honours `token:`, `nkey:<seed>` (in-memory
  `nats.Nkey` via `nkeys.FromSeed`) and `jwt:<jwt>+<seed>` (in-memory
  `nats.UserJWTAndSeed`) — no temp-file materialisation. Covered by
  embedded-server auth round-trip tests per scheme (nkey + decentralized JWT).
  This is client-side only: it has nothing to authenticate against until the
  broker itself requires auth (the lab `nats` chart runs auth-free, and there is
  no NATS broker chart in this repo).
- **Remaining — all gitops / out of this repo:**
  - Enable NKey/JWT on the NATS broker deployment (gitops's `nats` chart).
  - Document the credential-rotation flow. Deferred **with** the broker change,
    not before it: writing a rotation runbook for a scheme the broker doesn't
    yet enforce would drift. It belongs beside the gitops overlay that
    introduces the broker identities it rotates.
- **~~SeaweedFS-publisher auth~~ — MOOT under Garage.** The original DoD also
  required auth on the SeaweedFS filer's `gocdk_pub_sub` publisher
  (`gitops/.../seaweedfs/seaweed.yaml`, `NATS_SERVER_URL` from process env,
  no auth fields exposed by gocloud.dev's natspubsub driver). The dev cluster
  switched the `primary` backend off SeaweedFS onto **Garage** (see the
  ADR-0011 storage-migration note *"dev cluster object store is Garage"* in the
  Storage section) — Garage runs no SF filer→NATS publisher, so there is no SF
  publisher to authenticate. The in-repo SF *source* adapter
  (`internal/eventingest/source_seaweedfs_nats.go`) stays as a dormant,
  pluggable ingest format; if a SeaweedFS-class backend is ever reused this leg
  returns with it.
- **Trigger to do (revised):** a **non-lab deployment of the PALADIN dispatcher
  NATS sink** — the event fan-out bus (`paladin.bucket.updated` etc.), which runs
  independent of the object store and is live under Garage. That is what now
  needs broker-side auth so a stolen Pod identity can't fan out arbitrary
  events. The former trigger (a non-lab SF NATS publisher) is void while Garage
  is the store.

### Storage event ingest pipeline — JetStream upgrade + integration coverage

- **Status:** Deferred (parent concept SHIPPED — only follow-ups remain)
- **Dormant under Garage (2026-07-05):** this pipeline is SeaweedFS-specific
  (SF filer→NATS publisher). The dev cluster switched the `primary` backend off
  SeaweedFS onto **Garage** (see the *"dev cluster object store is Garage"* note
  in the Storage section), which emits no filer events, so the SF source is idle
  in the current cluster. The in-repo SF source adapter stays as a pluggable
  format for a possible SF-class backend return; the ~~SeaweedFS-publisher auth~~
  leg of the NATS-auth item above went moot for the same reason.
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
  [ADR-0011](backend/docs/adr/0011-per-tenant-bucket-layout.md); **Phase 3
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
  their initiate-time (backend, bucket) — migration 053 (#127).
- **Shipped — Phase 1 (dedicated layout):** `tenants.storage_layout`
  (migration 054, proto field 12, end-to-end #128); CreateTenant with
  `dedicated` provisions a pending tenant-owned bucket + default binding
  in the same tx, physically created by the (backend-routed) reconciler
  (#129); mutations are gated on `provision_state='ready'` with a clean
  retryable FailedPrecondition (#130). Provisioning now verifies the bucket
  is actually reachable (post-`CreateBucket` `HeadBucket`) before it can flip
  to `ready`, so a backend that accepts `CreateBucket` without yielding a
  writable bucket keeps the gate closed and surfaces `provision_error`
  instead of 500-ing every upload.
- **Shipped — Phase 3 slice 1 (same-backend copy job):** migration 056
  `tenant_storage_migrations` (resumable state machine + cursor); admin RPC
  `MigrateTenantStorageLayout` + `GetTenantStorageMigration` (provision the
  dedicated bucket + record the migration); `StorageMigrationWorker` (leased
  BackgroundJob) drives provisioning→copying (same-key server-side
  `CopyObject`, cursor-resumable)→transactional `object_keys` rebind + layout
  flip→verify→completed. Cross-backend pairs fail loudly (deferred to the
  stream-through slice). Source copies are RETAINED (cleanup is a later slice).
  Unit-tested state machine (happy path, bucket-wait, cross-backend fail).
- **Shipped — Phase 3 slice 2 (retention-gated cleanup):** migration 057 adds
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
- **Definition of Done (remaining — Phase 3 hardening):**
  - Live cross-backend run (needs a second working backend) + two-backend
    integration test of the full migration.
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
  PALADIN defect. Garage has no per-bucket volume reservation; verified
  end-to-end (shared security-probe 6/6 + dedicated provision→upload→read).
  **Garage prerequisite:** the S3 access key needs the global create-bucket
  grant (`garage key allow --create-bucket <key>`) or the ADR-0011
  reconciler's `CreateBucket` is rejected. **Follow-ups:** (1) to move the
  change into the registry-pulled chart, `task deploy:backend`, then re-enable
  the PALADIN ArgoCD app's `automated` sync (paused during the live cutover);
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

- **Status:** This-repo half SHIPPED (2026-07-01) — only the gitops
  controller install + the SealedSecrets-vs-Vault decision remain.
- **Shipped:** the chart already references pre-existing Secrets (the
  SealedSecrets contract). Closed the two gaps where a `*_secret` /
  `*_ref` field existed but the resolver never walked it, so a
  SealedSecret-backed prod deploy would have booted with an empty value:
  `K8sSecretResolver` now resolves `auth.signing_key_secret` (the JWT HMAC
  key) and `ingest.webhook.shared_secret_ref` (the storage-event webhook
  HMAC). `values.yaml` documents both SecretRef options and pre-allowlists
  their default names in `rbac.secretReader.secretNames`;
  `values-prod.yaml` moves the signing key off the inline placeholder onto
  `signing_key_secret`. `docs/security.md` §5 gained a `kubeseal --raw`
  runbook (strict-scoped per namespace+name) covering the signing key +
  webhook HMAC. Resolver tests pin both new resolutions.
- **Definition of Done (remaining):**
  - SealedSecrets controller installed via gitops (out of this repo).
  - The sealed YAML for each secret lives in gitops alongside the
    ApplicationSet.
- **Blockers:** decision between SealedSecrets vs external-secrets with
  Vault — an org call that gates the controller install.

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

- **Status:** Workflow AUTHORED (2026-07-02) — first green run + the
  required-check flip remain, both blocked on Actions billing.
- **Shipped:** `.github/workflows/e2e.yml` — builds both images from the
  deploy Dockerfiles (`:latest` tags the compose file references), starts a
  MinIO service as the S3 endpoint (compose containers reach it via the
  docker0 gateway, the Playwright host's presigned PUTs via localhost),
  installs pnpm + Chromium, runs `pnpm run test:e2e` (Playwright's webServer
  boots the compose stack itself), uploads the report artifact + stack logs
  on failure. Suite is currently 18/18 locally (incl. tenant switching).
- **Definition of Done (remaining):**
  - First green run on Actions — unverifiable until the account's billing
    is fixed (every run currently fails at startup with steps=0).
  - Flip to a required check alongside `test` / `security` (see the
    *Branch protection* entry — same billing gate).
- **Blockers:** GitHub Actions billing (account-level, repo owner only).


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
