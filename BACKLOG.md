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

### Role split: `auth-server` (OAuth / OIDC isolation)

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
  - **Integration tests for bucket / object_key / quota /
    object lifecycle handlers.** Today only
    `tenant_events_test.go` covers the seam end-to-end. The
    other four handler classes inherit the exact same
    pattern; the contract holds, but there's no regression
    safety in CI for any of them. Copy-paste from the tenant
    test, swap repo + handler + lifecycle method.
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
  - **`buckets/` prefix observation** — the `buckets/` strip in
    `parseSeaweedFSPath` was inferred from observed live
    paths; document the wire-format contract under `docs/`
    so a future SF version that drops the prefix or a
    different storage backend doesn't silently regress.
- **Trigger to act:** customer pipeline that writes directly
  to the storage bucket bypassing PALADIN RPCs (the entire
  raison d'être of the ingest plane), or production at-least-
  once requirement that needs JetStream.

---

## UI / Admin Console

### react-hooks v6: `set-state-in-effect` re-promotion

- **Status:** Deferred — gated on a data-fetching architecture change.
  (`immutability` + `preserve-manual-memoization` are DONE: their few real
  hits — forward references in `objects/page.tsx`, a memo-dep mismatch in
  `useObjectKeys.ts` — are fixed and both rules now enforce at `error`. Only
  `set-state-in-effect` remains demoted to `warn`.)
- **Reason:** the ~40 `set-state-in-effect` hits are the legitimate
  fetch-on-mount / init-from-browser pattern in the data hooks (`useObjects`,
  `useConfig`, `ScopeContext`, …). They are not lint suppressions to sprinkle
  — the rule wants the state derived during render or pushed into an external
  store, which is an architectural change, not a per-line fix.
- **Definition of Done:**
  - Adopt a uniform data-fetching primitive (TanStack Query or a small
    `useSyncExternalStore` store) so fetch state no longer lands via
    setState-in-effect.
  - `npx eslint .` reports zero `set-state-in-effect` hits.
  - Delete the remaining `"warn"` override in `eslint.config.mjs`.
- **Blockers:** none — but it is wide and must ride the data-fetching
  refactor to avoid 40 scattered `eslint-disable` lines.

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

- **Status:** Aspirational
- **Reason:** Migration 030 relaxed `object_key_format` so paths
  like `invoices/2026/q1` are valid alongside the old
  single-segment `assets-prod`. Read-side parsers in
  `internal/eventingest/source_*.go` still split incoming S3 keys
  as `<bucket>/<tenant_uuid>/<object_key>/<key>` with the third
  segment treated as the OK and the rest as the user-key. With
  multi-segment OK that's ambiguous: an event for path
  `<tenant>/invoices/2026/q1/report.pdf` could resolve as OK
  `invoices` + key `2026/q1/report.pdf` OR OK `invoices/2026/q1`
  + key `report.pdf`. Existing single-segment OKs are unaffected
  (split-on-`/` happens to land on the right segment); the
  ambiguity surfaces only once an operator creates a multi-segment
  OK and writes objects to it.
- **Definition of Done:**
  - Longest-prefix-match against `object_keys` rows for the tenant
    (cached per-tenant, invalidated on OK create/delete).
  - All four ingest sources (seaweedfs, seaweedfs-nats, minio,
    cloudevents) use the shared resolver.
  - Integration test covering OK precedence ordering when nested
    paths collide (e.g. `invoices` + `invoices/2026`).
- **Blockers:** none — pure backend refactor, no proto change.



### Phase 3: deprecate redundant resource-name shapes

- **Status:** Open — gated on the shape-distribution data the new metric now
  collects. The Phase-2 central resolver is DONE:
  `internal/api/connectshim/resolve` exports
  `ResolveObjectKeyName(ctx, name) (CanonicalRef, error)` handling all three
  shapes (canonical A / tenant C / bare B; bare takes the tenant from ctx)
  plus `ResolveTenantParent`, and every objectKey call site in
  `connectshim/admin/object_key_server.go` swapped to it (the local
  `objectKeyParts` / `tenantUUIDFromParent` are gone). The
  `paladin_resource_name_shape_total{shape}` counter records which shape each
  request used. Unit tests cover all shapes + error paths.
- **Reason this remains:** once the metric shows the real-world distribution,
  decide whether to deprecate a shape (e.g. drop bare B if nobody sends it) and
  whether the data plane's object-name parser
  (`connectshim/data/conv.go::objectNameParts`, a different
  `…/objects/{id}` shape) should move behind the same resolver.
- **Definition of Done:** a deprecation decision per shape backed by ≥1 week of
  `paladin_resource_name_shape_total` data; optionally fold the data-plane
  object-name parse into the resolver.
- **Blockers:** needs production traffic through the new metric first.

### Cedar policy templates: canonical resource literals

- **Status:** Aspirational
- **Reason:** Phase 1 of `backend/docs/canonical-resource-names.md`
  switches audit_log and event payload `resource_name` to the A-shape
  `storageBackends/{b}/buckets/{bk}/tenants/{tid}/objectKeys/{ok}`.
  The default policy template (`internal/api/v1/tenant/defaultpolicy.go`)
  uses unconstrained `resource` so it's untouched. Operator-authored
  policies that reference resources by C-shape EUID
  (`ObjectKey::"tenants/{tid}/objectKeys/{ok}"`) keep working — the
  Cedar evaluator's `cedar.Resource` builder still emits the C-shape
  EUID at evaluation time. Migrating those EUIDs to canonical is a
  separate decision that touches operator-written policies in
  `tenants.inherited_cedar_policy` + `object_keys.cedar_policy`.
- **Definition of Done:**
  - `cedar.Resource` builds canonical EUID for ObjectKey-rooted
    resources.
  - Rewrite pass over `tenants.inherited_cedar_policy` +
    `object_keys.cedar_policy` (similar shape to the slug-rename
    rewrite in `adapters/tenant.go`) translates existing C-shape
    EUIDs to canonical.
  - Cedar authoring docs (`backend/docs/cedar-authoring.md`)
    updated to show the canonical EUID form.
- **Blockers:** none technical; needs a deploy window so the
  policy-rewrite pass can run before clients start receiving
  canonical-EUID authz decisions.

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

### API-test fixture for UI/UX with real-shape data

- **Status:** Deferred
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
- **Outstanding (load + stress flavours):**
  - `--flavour=load` — ~10k objects, ~100 deliveries spread
    across 24h so `/billing` time-series buckets look real
    and `/events` Last-test column has a population to truncate.
    Requires bucket + objectKey + UploadObject + CompleteObject
    flow that demo doesn't exercise yet.
  - `--flavour=stress` — pagination boundaries (1000 / 1001 /
    1099 rows so the cursor logic gets exercised). Same
    upload-flow gap.
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

### CNPG HA replicas in the PALADIN chart

- **Status:** Resolved-as-out-of-scope — see [ADR-0005](docs/adr/0005-cnpg-ha-ownership.md). The PALADIN chart does NOT own the CNPG `Cluster` (it lives in gitops); HA config belongs there. The chart's verify-full TLS half shipped. The original premise below ("chart already templates cluster.yaml") was incorrect.
- **Reason:** Minikube runs a single-instance CNPG `Cluster` and the
  PALADIN chart accepts that as the default. Production-class deploys
  need ≥2 replicas with synchronous quorum, a PodDisruptionBudget,
  and an explicit failover policy. The chart already templates
  `cluster.yaml` but doesn't expose `instances`, `minSyncReplicas`,
  `maxSyncReplicas`, or `affinity`/`topologySpreadConstraints` as
  Helm values — operators today hand-edit the generated cluster.
- **Definition of Done:**
  - `values.yaml` adds a `postgres.ha` block:
    `instances`, `minSyncReplicas`, `maxSyncReplicas`,
    `synchronousReplication.method`, `pdb.minAvailable`.
  - `values-prod.yaml` ships sane HA defaults (3 instances, sync
    quorum 1).
  - `templates/cluster.yaml` wires the block into the CNPG
    `Cluster.spec.{instances, minSyncReplicas, maxSyncReplicas,
    affinity, topologySpreadConstraints}` and adds a sibling
    PodDisruptionBudget gated by `postgres.ha.pdb.enabled`.
  - `backend-chart-verify` lefthook task gains a snapshot test
    that diff's the rendered Cluster against committed golden
    output for each environment.
- **Blockers:** none.

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

### golangci-lint follow-ups (adopt deferred linters)

- **Status:** Deferred (the gate is LIVE; this is incremental adoption)
- **Reason:** `golangci-lint run ./...` now exits 0 on a curated config and
  runs in CI (`test.yml`) + lefthook pre-push. The config was trimmed to the
  high-value correctness/security core (`standard` = errcheck/govet/
  ineffassign/staticcheck, plus bodyclose/rowserrcheck/sqlclosecheck/
  durationcheck/makezero/misspell/testifylint/…) and the genuine findings in
  that set were fixed. Several valuable linters were deliberately left OFF
  because each needs its own fix pass first — adopting them is the remaining
  work.
- **Definition of Done (remaining), each its own no-behaviour-change pass:**
  - `unused` — DONE (2026-06-26): removed the 9 dead items (5× non-Tx
    `dispatchEvent` twins superseded by the ADR-0003 tx-seam, the
    `deliverHTTP` wrapper, the `statelessSTSClient` interface, and the
    `fakeRequest` test scaffold + its keep-alive) and dropped `unused` from
    the `disable` list. Gate is green with it on.
  - `nilerr` — EVALUATED AND REJECTED: all 14 hits are deliberate,
    already-commented fail-open / skip patterns (limiter fail-open, quota
    "degrade open", restore not blocked on a meta-lookup miss, strict-loader
    deferring to the main parse error, policy `Validate` returning the error as
    a string *result*). Enabling it means 14 `//nolint` for zero bugs — not
    worth the noise. Leave OFF.
  - `errorlint` — DONE: 11 hits fixed (9× `%v`→`%w` to preserve the error
    chain incl. multi-`%w` for the "compensation also failed" sites; `==` →
    `errors.Is` in serve_worker; type-assert → `errors.As` in a celh test) and
    added to `enable`.
  - `noctx` — DONE (2026-06-28): the 28 `httptest.NewRequest` /
    `http.NewRequest` test call sites now use `…NewRequestWithContext`
    (`t.Context()`, or `context.Background()` in the one `t`-less helper).
    Added to `enable`; gate green.
  - `contextcheck` — EVALUATED AND REJECTED: all 9 hits are deliberate.
    Four are the canonical graceful-shutdown idiom (`context.WithTimeout(
    context.Background(), …)` created *after* `<-ctx.Done()` — you can't
    derive from the already-canceled parent: serve_ingest, serve_mcp,
    driver_webhook, the flushOTel/Shutdown teardown). Two are `config.Load`
    (synchronous boot-time file I/O, ctx-less by design). The rest are
    `container.Run()`/`Shutdown()` lifecycle methods and `DecodeRefresh`
    (pure JWT decode, no I/O). Enabling it means ~9 `//nolint` for zero
    bugs — same calculus as `nilerr`. Leave OFF.
  - `gosec` — DONE (2026-06-28): enabled. The G115 rule (int→int32/uint32
    narrowing) is excluded in `settings.gosec.excludes` — it has no range
    analysis and all 26 hits are pagination totals / counts / lengths bounded
    by query results or config (zero overflow risk, per-site nolint would be
    pure noise). `_test\.go` is excluded (test G304 file-inclusion + G101
    throwaway-cred hits aren't a runtime surface). Three production hits are
    `//nolint`'d with reasons: G101 on the `X-PALADIN-API-Token` header const,
    G118 on the two graceful-shutdown goroutines (detached ctx after parent
    cancel), G709 on the internal SeaweedFS→NATS gob envelope. The rest of
    gosec (command injection, weak crypto, file perms, …) stays active.
  - `errchkjson` — EVALUATED AND REJECTED: the 15 hits are app-controlled
    marshals that can't fail in practice — proto sink config, structs with
    `uuid.UUID`/`time.Time`/`any` (all marshalable), static metadata docs —
    or `Encode` to an `http.ResponseWriter` with no recovery after the header
    is written. Adopting needs ~12 `//nolint` for ~0 real risk (same calculus
    as `nilerr`). The one genuine smell it surfaced — `batch` marshaling its
    args, dropping the error, then submitting the possibly-nil result — was
    fixed independently (the three `Batch*` handlers now return on a marshal
    error). Leave OFF.
  - pgx `BeforeAcquire` → `PrepareConn` — DONE (2026-06-28). The old
    "needs rethinking" note was wrong: in pgx v5.10 `PrepareConn` is also a
    *per-acquire* hook (same timing as `BeforeAcquire`), just with a
    `(bool, error)` result, so `EnableRLS` swapped to it directly. The new
    error return is an upgrade: a failed `set_config('paladin.tenant_id', …)` now
    surfaces as the real error on the instigating query — `(false, err)`
    destroys the suspect conn — instead of the old silent retry until "too
    many failed attempts". `AfterRelease` is not deprecated, unchanged.
    `//nolint:staticcheck` removed; RLS integration tests green.
    (`h2c.NewHandler` → `http.Server.Protocols` was already DONE.)
  - The pure-style linters (`nlreturn` ~1.5k, `goconst`, `predeclared`,
    `nestif`, `dupl`, …) stay OFF by design — not worth the churn.
- **Blockers:** none — incremental, each linter independently adoptable.

### Playwright e2e suite wired into CI

- **Status:** Deferred
- **Reason:** the e2e compose stack now boots locally (see "Frontend
  Playwright suite — runtime sign-off" under Testing / E2E), but the
  suite has not yet earned its flake budget (SC-002/SC-003), so gating
  PRs on it would block merges on known-unstable signal.
- **Definition of Done:**
  - Runtime sign-off entry above is closed (suite passes 10×
    consecutively, <3 min wall-clock).
  - `.github/workflows/e2e.yml` builds both images, boots
    `tests/e2e/docker-compose.test.yaml`, runs `pnpm run test:e2e`,
    and uploads the Playwright report as an artifact on failure.
  - Workflow is required for merge alongside `test` / `security`.
- **Blockers:** [[Frontend Playwright suite runtime sign-off]] —
  SC-002/SC-003/SC-004 must pass locally first.


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
  `required_status_checks` for `backend` / `frontend` / `gitleaks` /
  `trivy-fs` (strict) via
  `gh api -X PUT repos/:owner/:repo/branches/<br>/protection`.
- **Blockers:** GitHub Actions billing — the runner accepts each job then
  fails instantly (Settings → Billing → spending limit / payment method).
  Account-level; only the repo owner can resolve it.


---

## Documentation



## Storage backends

### Backend states beyond enable/disable (drain, maintenance, bulk)

- **Status:** Aspirational
- **Reason:** Feature 002 (`specs/002-backend-enable-disable`) shipped a
  two-value `enabled` flag with strict reject semantics: a disabled
  backend refuses ALL PALADIN-mediated ops. Three richer behaviours were
  deliberately scoped out to keep v1 small and unambiguous.
- **Definition of Done:**
  - **Read-only drain mode** — a distinct backend state that blocks
    writes / presign-PUT / multipart-init but still allows reads /
    presign-GET, so an operator can migrate data off a backend before
    fully disabling it. Needs a tri-state (or separate column) on
    `storage_backends` and a split in the resolver gate
    (`object.MapResolveErr`) by operation class.
  - **Richer lifecycle states** — draining / maintenance / error;
    e.g. auto-set `error` when `TestBackend` fails, surfaced in the UI.
  - **Bulk enable/disable** — toggle multiple backends in one action
    (admin UI multi-select + a batch RPC or client-side fan-out).
- **Blockers:** none technical; deferred purely for v1 scope. Drain mode
  is the most-requested next step (data migration off a backend).

---

## Testing / E2E

### Frontend Playwright suite — runtime sign-off (SC-002 / SC-003 / SC-004)

- **Status:** Blocked
- **Reason:** The Playwright E2E suite (`specs/001-frontend-playwright-e2e`)
  is authored and passes all static gates (`tsc --noEmit` clean, all
  scenarios discovered), but the runtime verification tasks
  (T012, T018, T023, T029, T034, T038, T041, T042) were deferred during
  `/speckit-implement`: the PALADIN backend / UI containers
  (`registry.local/paladin/paladin:latest`,
  `:paladin-ui:latest`) were not buildable/reachable from
  the impl host's Docker daemon, so the test-stack never booted.
- **Definition of Done:**
  - Build the PALADIN images locally (`task -d backend build:image` or
    equivalent) and port-forward the external Garage with
    `PALADIN_E2E_S3_ACCESS_KEY/_SECRET_KEY` set.
  - T012: `pnpm run test:e2e:stack` reaches `healthy` on all six
    compose services within 60 s.
  - SC-003: each `*.spec.ts` passes 10× consecutively with zero flake.
  - SC-002: the full suite run completes in <3 min wall-clock.
  - SC-004: the five intentional-break dry-runs from quickstart each
    produce a clear named failure in the expected spec file.
  - Flip T012/T018/T023/T029/T034/T038/T041/T042 from `[~]` to `[X]`
    and delete this entry in the same commit.
- **Blockers:** ~~PALADIN image availability + reachable S3~~ — RESOLVED
  during feature 002's T032 sign-off. The images build locally
  (`docker build -f {backend,frontend}/deploy/Dockerfile`), a local
  Garage v2.3 at `:3900` supplies S3, and four never-before-run stack
  bugs were fixed (dup `bootstrap.admin` password+secret; distroless
  `wget` healthchecks; UI BFF env-var name mismatch `PALADIN_BACKEND_URLS_*`
  → `PALADIN_{DATA,IAM,ADMIN}_URL`; e2e seed transport missing
  `Idempotency-Key`). The stack now boots and `backend-disabled.spec.ts`
  passes against it. Remaining 001 work is purely running its own five
  specs 10× for the flake/timing budget (SC-002/SC-003) and the
  intentional-break dry-runs (SC-004) — no longer infra-blocked.
