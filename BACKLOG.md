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

### Push renamed charts/images before syncing the renamed ApplicationSet

- **Status:** Blocked (operator action — cannot deploy from a coding session).
- **Reason:** The project was renamed from `paladin` / `paladin` to
  `paladin` (2026-08-19). Chart names, image repos, Helm release/app names and
  every in-cluster resource name follow: `paladin-core` / `paladin-console`.
  This supersedes the earlier, never-completed
  `paladin`→`paladin-core` cutover — do NOT run that one; go
  straight from whatever is deployed to the `paladin-*` names, so the cluster
  takes one disruption instead of two.
  The ArgoCD ApplicationSet must pull
  `oci://registry.local/charts/{paladin-core,paladin-console}` and images
  `registry.local/paladin/{paladin-core,paladin-console}`. Until those exist, a
  sync cannot resolve its sources.
- **Definition of Done:**
  - Build+push the renamed charts and images first (`task -d backend deploy`,
    `task -d frontend deploy`) so `charts/paladin-core`, `charts/paladin-console`
    and the `paladin/{paladin-core,paladin-console}` images exist in the registry.
  - Update the gitops ApplicationSet + overlays to the new names, then sync.
    Because the release name changed, the old `paladin*` /
    `paladin-*` Deployments/Services/ServiceAccounts/Certificates/Linkerd Servers
    are pruned and new `paladin-core-*` / `paladin-console` ones created — mTLS
    certs (SANs `paladin-core-api`/`paladin-core-admin`, SPIFFE
    `…/sa/paladin-core-api`) regenerate. Expect a brief in-namespace
    disruption; confirm the `/login` redirect, BFF→backend health aggregation
    and internal mTLS all recover.
  - Reprovision the database rather than migrating it — the maintainer's
    call, 2026-08-19. Follow [docs/upgrading.md](docs/upgrading.md): drop the
    old database and roles, let `migrate` build the schema from empty, run
    `bootstrap`, reissue every API token, and rewrite every `PALADIN_*` variable
    in the overlays to `PALADIN_*` BEFORE the new pods start. The control-plane
    rows (tenants, buckets, ObjectKeys, capabilities, subscriptions, audit
    log) are lost by design; object bytes in the S3 backend survive but are
    orphaned, and Paladin will not adopt them on its own.
  - Delete the orphaned `charts/{paladin*,paladin-*}` OCI repos and
    the matching images once the cutover is verified.
  - Delete this entry when the cutover is done and verified.
- **Blockers:** operator must run the deploy + sync; not automatable from here.

### The `paladin` name collides — decide qualify-vs-rename before publishing

- **Status:** Open, and now informed. Registries checked 2026-08-21; the
  trademark question is still open and is the one that matters most.
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
- **Blockers:** none technically. This gates *publishing*, not development —
  see the CI item, which wants the repository public.

---


## MCP bridge

### Tool-coverage gaps vs the Paladin RPC surface

- **Status:** Deferred (partial — the data-plane cluster + admin read gaps
  landed 2026-06-26; 59 tools). Remaining items below need a product call.
- **Reason:** The MCP bridge (`internal/mcp/bridge.go`) exposes a curated
  subset of the ~110 Paladin RPCs (59 tools). `DefaultCatalog` in
  `internal/mcp/profile.go` is the ground-truth list and is pinned to the
  real registrations by `TestServerRegistersDefaultCatalog`. Now wired:
  `UpdateObject`, `DeleteObjectTags`, `ListDistinctTags`, `BatchUpdateTags`,
  `RegenerateUploadUrl`, the 5 `MultipartUploadService` RPCs, data
  `CancelOperation`, admin `ResetUsage` / `GetAuditLogEntry` / `GetConfig`
  (`SystemService` client added). The agent-usable upload/tag mutations are
  in `agent_safe`; `ResetUsage` / `system_config` / batch tools stay
  admin-only. The following Paladin capabilities are still **not** reachable
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

### Domains still uninstrumented

- **Status:** Deferred (each is small; none is on the critical path today).
- **Reason:** presign, capability charge and object lock are now counted, and
  otelconnect gives RED for every RPC. Four domains still emit nothing of their
  own: storage-backend call latency and errors (an S3 that degrades shows up
  only as slow RPCs), quota enforcement decisions, login success/failure rates,
  and idempotency-key hits. None blocks an incident today because the RPC-level
  signal covers the symptom, but each answers a different "why".
- **Definition of Done:** a counter per domain with a bounded outcome label,
  reaching a collector under test the way `internal/metrics/domain_test.go`
  checks the existing three.
- **Blockers:** none; ordering is by whichever incident asks first.

### Nothing notices when the e2e environment fills up from outside the suite

- **Status:** Deferred (the suite no longer contributes; other paths still can).
- **Reason:** The suite used to leave every tenant, bucket, collection and
  object it created, and at ~100 tenants it stopped passing — as three
  unrelated-looking failures (a scope assertion matching the wrong "Switch
  Target", a trash row that had not rendered, a list that stopped showing a
  freshly seeded tenant), each fix moving the failure one step earlier. That
  is fixed: teardown walks the RESTRICT edges and removes what a test created,
  including rows typed into the console's own dialogs, and a full run now
  leaves the counts exactly where it found them. What is still missing is a
  floor under the assumption: an aborted run, a manual experiment, or a
  half-finished migration can still leave rows, and the suite will keep
  passing until the pile is large enough to fail in that same misleading way.
- **Definition of Done:** A check that fails loudly when fixture-shaped rows
  cross a threshold — run before the suite, so the next person reads "the
  environment is full" instead of debugging a scope-picker assertion.
- **Blockers:** none. Wants a definition of "fixture-shaped" that does not
  also match a real tenant: the `e2e-` / `switch-` prefixes are the obvious
  candidate, and they are a convention nothing enforces.

### `housekeeping.operations_ttl` is 14 days because the chart says so

- **Status:** Deferred (a number to agree on, not a defect).
- **Reason:** schema.cue defaults it to 720h and the chart now sets 336h. The
  chart value was added while chasing a stale row on the failed-operations
  widget, on the mistaken belief that the key being absent left the reaper
  switched off — it did not: CUE supplied 720h and the reaper has always run.
  The row was on the widget because the listing sorted ascending, which is
  fixed separately. So 14 days is a retention change nobody has weighed:
  shorter is friendlier to the widget, longer is friendlier to a post-mortem.
- **Definition of Done:** Either a deliberate decision recorded next to the
  value, or the chart line goes and 720h stands.
- **Blockers:** none. Wants an opinion on how far back an operator should be
  able to read a batch job's outcome.

### Tables carrying `tenant_id` with no RLS policy

- **Status:** Blocked (needs the pre-auth read path designed, like `api_tokens` has).
- **Reason:** `002_roles_and_rls.sql` documents why `tenants` and
  `storage_backends` have no policy — platform-level, no owning tenant. Four
  tables carry a `tenant_id` column with no policy and no such note: `users`,
  `refresh_tokens`, `user_settings`, `tenant_default_bindings`. Silence is not
  a decision; a reader cannot tell an exemption from an omission.
  Found while covering `capability_revocations`, which turned out to be a real
  gap rather than an intended one — a tenant could revoke another tenant's
  capability (fixed in `004_capability_revocation_rls.sql`).
  `tests/integration/rls_coverage_gate_test.go` now enumerates the whole set
  from `pg_policies` and fails on anything not in an explicit allow-list, so
  the next table added without a policy fails a test rather than a review. It
  immediately turned up two more the manual sweep had missed
  (`oauth_authorization_codes`, `tenant_slug_history`) — both legitimate
  pre-auth reads, now documented as such in the allow-list rather than
  inferred from silence. `api_token_rate_buckets` has no `tenant_id` column at
  all, so it is subordinate rather than unprotected.
- **Definition of Done:**
  - Each of the five either gets a policy, or gets a comment in the RLS
    migration saying why it cannot have one.
  - `users` is the hard case and sets the pattern: login reads the row
    *before* the tenant is known, which is exactly the constraint `api_tokens`
    already solves with a second, column-narrowed pre-auth policy. Mirror it
    rather than inventing a second shape.
  - `api_token_rate_buckets` is subordinate to `api_tokens`; isolate it
    through the parent the way `multipart_parts` and `capability_usage` do.
- **Blockers:** the `users` pre-auth path needs tracing before a policy can be
  written without breaking login.

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
  external IdP is **not needed now**. `auth.jwks_url` + the stub
  `auth.NewJWKSVerifier` constructor (wired in `cmd/server/root.go`)
  stay inert and harmless — every JWT today is HS256 minted by the IAM
  plane itself.
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

### cel-go moved to `cel.dev/cel-go` — pinned at v0.31.0 until protovalidate follows

- **Status:** Blocked on an upstream dependency. Noted 2026-08-21.
- **Reason:** cel-go v0.32.0 declares `module cel.dev/cel-go`, so
  `go get github.com/google/cel-go@v0.32.0` fails with "module declares its
  path as: cel.dev/cel-go" and restores v0.31.0. The failure reads like a
  broken dependency; it is a module rename. v0.31.0 is the last release under
  the old path, and it is what we are on. (The upstream repository is also
  moving to `github.com/cel-expr/cel-go` in June 2026 — a third address for
  the same code, which is why the import path, not the repo URL, is what to
  track.)
- **Why not just switch the import path now:** `buf.build/go/protovalidate`
  — currently v1.3.0, the latest — still requires
  `github.com/google/cel-go@v0.30.0`. Go treats the two paths as unrelated
  modules, so switching ours would compile *two* copies of CEL into every
  binary. Nothing we need is in v0.32.0, so the cost buys nothing today. Our
  own `internal/filter/cel` and protovalidate do not exchange CEL types, so
  the duplication would be wasteful rather than incorrect — but it is still
  waste.
- **Definition of Done:** when protovalidate requires `cel.dev/cel-go`,
  rewrite the four imports under `internal/filter/cel/`, run
  `go get cel.dev/cel-go@latest && go mod tidy`, and drop the Renovate pin in
  `renovate.json`. Confirm `go mod graph | grep cel-go` shows one module.
- **Blockers:** protovalidate's own migration. Renovate is pinned to
  `<=0.31.0` so the monthly batch does not keep proposing an update that
  cannot resolve.

---

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

### Replication: real `StorageReplicator` implementation

- **Status:** Aspirational
- **Reason:** [internal/worker/replication.go](backend/internal/worker/replication.go)
  walks replicated buckets and logs intent; [cmd/server/root.go](backend/cmd/server/root.go)
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

### Event dispatcher: RLS regression test for the cross-tenant subscription lookup

- **Status:** Deferred (the bug itself is FIXED — this is the missing regression).
- **Reason:** The OutboxRunner drains `event_deliveries` cross-tenant on the
  BYPASSRLS `dispatcherPool`, then per row calls `Store.Get(subID)` to read the
  sink config. That store was wired to `deps.Repos.EventSub` (the RLS-scoped
  runtime `paladin_app` pool); the drain loop sets no `paladin.tenant_id` GUC, so RLS
  hid every subscription and the runner marked **all** deliveries
  `"subscription deleted"` — the entire sink-delivery path was dead whenever
  `audit_mirror`/`charge` events (or any real subscription) were live. Found by
  the first end-to-end delivery test (2026-07-05); fixed in `serve_dispatcher.go`
  by binding the store to `dispatcherPool` (`adapters.NewEventSubscriptionRepoV2(
  sqlc.New(dispatcherPool))`). No test caught it because
  `tests/integration/dispatcher_test.go` wires its store to `PoolMigrate`
  (BYPASSRLS), masking the exact condition, and the feature was off by default.
- **Definition of Done:** an integration case (pgharness) that reproduces the
  production condition — a subscription owned by tenant A, an `event_deliveries`
  row for it, and an OutboxRunner whose subscription store runs on the RLS
  `paladin_app` pool with **no** `paladin.tenant_id` GUC — asserting the delivery
  succeeds (proving the store is BYPASSRLS), and a sibling asserting the
  RLS-scoped/no-GUC store fails `"subscription deleted"` so the invariant is
  pinned. Faithful "no-GUC" reproduction needs an `paladin_app` pool WITHOUT the
  harness's WithRLS GUC hook — the piece that makes this more than a one-liner.
- **Blockers:** none — needs the harness to expose a GUC-less `paladin_app` pool.

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
- **Shipped (2026-07-05) — batch fan-in (NATS + Kafka):** the OutboxRunner now
  groups a tick's rows by sink target and flushes each group once, matching the
  SQS `SendMessageBatch` path. Kafka: `kafkaGroupTarget` keys by
  brokers+topic+auth-hash and `deliverKafkaBatch` sends one `WriteMessages(msgs
  ...)` per group (kafka-go batches to the broker internally; a `WriteErrors`
  slice maps partial failures per-row, any other error fails the whole group
  retryably). NATS: `natsGroupTarget` keys by the CONNECTION (url + raw
  credentials_ref, NOT subject — one Flush per conn covers every subject), and
  `deliverNATSBatch` does N publishes + ONE `FlushTimeout` (a publish error
  fails just that row; a flush error fails every row that published). Grouping is
  over RAW config so it never merges distinct-credential sinks; malformed rows
  fall back to the per-row `deliver()` path unchanged. Every row keeps its own
  attempts/backoff/permanent bookkeeping via the per-row outcome map. Unit tests
  (`sink_batch_test.go`): kafka one-call-per-group / partial-failure / whole-call
  -fail + group-key identity; nats embedded-server multi-subject delivery +
  no-pool + group-key (subject-independent).
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

- **Status:** Partially done — the Paladin-side (client) half shipped 2026-06-29.
  The remaining work is **broker-side** (enable auth on the NATS broker), which
  lives in gitops (separate repo). Two client-side surfaces publish to that
  broker unauthenticated today: the Paladin dispatcher NATS sink **and** the
  SeaweedFS filer→NATS publisher — both verified live 2026-07-05 (see below).
- **Correction (2026-07-05):** an earlier revision of this entry claimed the
  SeaweedFS-publisher leg was *moot because the cluster moved to Garage*. That
  was wrong. Garage became the `primary` backend, but SeaweedFS is still
  deployed as **`secondary`** (`seaweedfs-filer.storage:8333`) with its
  `gocdk_pub_sub` publisher active, and the full SF→NATS→Paladin ingest pipeline is
  live. The "Garage" move was a `primary`-pointer change, not an SF teardown.
- **Shipped (this repo):** the outbound `Dispatcher` → NATS sink supports NKey /
  JWT. `NatsSink.credentials_ref` honours `token:`, `nkey:<seed>` (in-memory
  `nats.Nkey` via `nkeys.FromSeed`) and `jwt:<jwt>+<seed>` (in-memory
  `nats.UserJWTAndSeed`) — no temp-file materialisation. Covered by
  embedded-server auth round-trip tests per scheme (nkey + decentralized JWT).
  This is client-side only: it has nothing to authenticate against until the
  broker itself requires auth (the lab `nats` chart runs auth-free, and there is
  no NATS broker chart in this repo).
- **Still unauthenticated (both live in the lab cluster):**
  - **SeaweedFS filer publisher** — `notification.toml`
    `[notification.gocdk_pub_sub] topic_url = "nats://seaweedfs.filer"`, dialing
    `NATS_SERVER_URL=nats://nats.nats.svc.cluster.local:4222` with no
    credentials (gocloud.dev's natspubsub driver surfaces no auth fields). Fires
    on every filer write; Paladin ingest consumes on subject `seaweedfs.filer`.
  - **The broker itself** — the gitops `nats` chart runs auth-free, so the
    dispatcher's shipped NKey/JWT support has nothing to authenticate against.
- **Definition of Done (remaining — all gitops / out of this repo):**
  - Enable NKey/JWT on the NATS broker deployment (gitops's `nats` chart).
  - Give the SF filer credentials for the authed broker: `NATS_SERVER_URL`
    carries them as `nats://<token>@host:port` (simplest flavour) via a
    Kubernetes `Secret`, since gocloud.dev exposes no structured auth fields.
  - Document the credential-rotation flow. Deferred **with** the broker change,
    not before it — a rotation runbook for a scheme the broker doesn't yet
    enforce would drift.
- **Trigger to do:** before any non-lab deployment — both the dispatcher sink
  and the SF publisher fan events into an unauthenticated broker, so a stolen
  Pod identity could publish arbitrary events. Fine for the lab (NATS has no
  exposed ingress, cluster-network only).

### Storage event ingest pipeline — JetStream upgrade + integration coverage

- **Status:** Deferred (parent concept SHIPPED — only follow-ups remain)
- **Live in the lab cluster — re-verified 2026-07-05.** SeaweedFS is deployed as
  the **`secondary`** backend (Garage is `primary`), and the SF filer→NATS→Paladin
  ingest pipeline is running: a live S3 PUT to the SF gateway made the filer
  publish to NATS `seaweedfs.filer` (broker `in_msgs` incremented), the broker
  delivered to the `paladin-ingest` subscriber (`out_msgs` incremented), and the
  ingest handler decoded the gob+protobuf envelope, classified it
  `paladin.object.uploaded`, parsed the `<bucket>/<tenant_uuid>/<object_key>/<key>`
  path, and ran the DB lookup (logged `ingest.handler "no matching object for
  event; skipping"` for a probe with no matching Paladin row). Transport + decode +
  parse + lookup all confirmed. (An earlier note calling this "dormant under
  Garage" was wrong — corrected here and in the NATS-auth item above.)
- **State as of 2026-05-10:** Full SF → NATS → Paladin ingest pipeline
  works end-to-end on the local cluster, **including PROMOTE on
  a real Paladin data-plane upload**. Verified live:
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
  - ~~**JetStream upgrade** — flip ingest from core pubsub
    (at-most-once) to a durable consumer (at-least-once).~~
    **DONE (2026-07-06):** `runJetStream` in `driver_nats.go` now
    has integration coverage (`driver_nats_jetstream_test.go`:
    deliver+ack, NAK→redelivery, ignored→ack, `Nats-Msg-Id`
    override), and the lab is live in JetStream mode. gitops:
    `base/nats/nats/seaweedfs-filer-stream.yaml` provisions the
    `seaweedfs_filer` stream (512MB; PALADIN_EVENTS capped 5GB→3GiB so
    the two share the 5Gi file store — a single stream reserving
    the whole budget fails 10047), and the ingest overlay sets
    `jetstream: true` + `durable_name: paladin-ingest-sf`. Verified:
    consumer bound, a live filer event delivered + acked (stream
    seq 1, 0 redelivered). Landed stream-first to avoid crashloop.
  - ~~**MinIO / S3 source** — decode S3-compatible bucket
    notifications.~~ **DONE (2026-07-06):** generalised the MinIO
    adapter into the canonical `S3EventSource` (`source_s3.go`) —
    parses the AWS S3 event-notification JSON emitted by AWS S3,
    MinIO, and any S3-compatible store; proper `url.QueryUnescape`
    key decoding (AWS-literal-slash + MinIO-`%2F` + `+`→space).
    `source_format` `s3` and `minio` both resolve to it (distinct
    labels); `/webhook/s3` + `/webhook/minio` routes. Covered by
    `source_s3_test.go` (13 cases: AWS+MinIO shapes, event
    variants, url-decode, bucket filter, non-Paladin key, id
    stability) + `pick_source_test.go`. **Garage is explicitly
    rejected** — it emits no notifications (Get/PutBucketNotification
    Configuration are 501; no non-S3 event mechanism), so
    `pickSource` errors with a directive pointing at the Reconciler.
    The native **SQS driver** (AWS S3 → SQS, long-polled) landed too
    (`driver_sqs.go`, `driver=sqs`): delete-on-success/ignore,
    leave-on-transient-error (→ redrive-policy DLQ), drop-poison on
    unrecognised, optional SNS unwrap, IRSA/AssumeRole/endpoint config;
    covered by `driver_sqs_test.go` + `build_sqs_driver_test.go`. All
    documented in [`docs/storage-ingest.md`](docs/storage-ingest.md).
  - ~~`buckets/` prefix observation — document the wire-format contract.~~
    **DONE (2026-06-29):** [`docs/storage-ingest.md`](docs/storage-ingest.md)
    documents the SF→NATS path contract (`<tenant>/<object_key>/<key>`), why
    the `buckets/` prefix is stripped (two publishers disagree on it), the
    drift risk, delivery semantics, and the MinIO path.
- **Trigger to act:** customer pipeline that writes directly
  to the storage bucket bypassing Paladin RPCs (the entire
  raison d'être of the ingest plane), or production at-least-
  once requirement that needs JetStream.

---

## Storage

### LifecycleHardDeleter is off in this deployment — 21 DELETED objects hold their bytes

- **Status:** Blocked (operator decision — a coding session cannot make the
  call, and the default is deliberate).
- **Reason:** `build_jobs.go` registers the hard-deleter only when
  `worker.jobs.housekeeping.hard_delete_after > 0`. The shipped default is
  `"0s"`, documented in `configs/config.yaml` as "0 disables (audit-only, dev
  default) — 7d-30d in prod is the typical setting". So this is working as
  designed for dev, not a bug. The consequence is still real: soft-deleted
  objects keep their bytes forever, and the deployment currently has 21 rows
  in `DELETED` doing exactly that.
  Unrelated to the permanent-delete leak fixed by migration 069 — that path
  now carries its own durable debt. This is the *soft*-delete cascade, and
  turning it on is a data-retention decision, not a correctness one.
- **Definition of Done:**
  - A decision per environment on the cooling-off window, written down: dev
    stays 0 or gets a short window; prod gets the 7d-30d the config already
    recommends.
  - When it is enabled, the existing 21 rows drain on the first sweep — check
    the count and the `hard-deleted` log lines before and after, because that
    first sweep reclaims a backlog rather than a trickle.
  - Note that enabling it also starts emitting `paladin.object.purged` from the
    lifecycle path; any consumer counting those events sees a burst.
- **Blockers:** needs the retention answer for each environment.


### A bodiless 404 cannot separate "no such object" from "no such bucket"

- **Status:** Deferred (known limitation, pinned by a characterization test).
- **Reason:** `s3adapter.notFound` classifies a HEAD failure so ReconcilerV2
  can decide between retrying and the terminal PENDING → FAILED. It rejects
  bucket-level error codes before accepting anything — but a HEAD response
  carries no body, so aws-sdk-go-v2's HeadObject deserialiser never reads a
  code and synthesises `*s3types.NotFound` from the 404 status alone. A
  backend answering "that bucket is gone" is therefore byte-identical to "that
  key is gone", and the reject list never sees it.
  Consequence: a wrong or deleted bucket binding makes the reconciler mark
  that binding's pending-expired objects FAILED. Recoverable **today** —
  nothing reclaims FAILED object bytes (the lifecycle hard-deleter filters on
  `state='DELETED'`, housekeeping's `pending_ttl` sweep on `PENDING`), so both
  the row and the bytes survive for an operator to re-promote. It stops being
  recoverable the moment anything reaps FAILED.
  `TestHeadCannotDistinguishMissingBucket` asserts the current behaviour so
  the gap is visible and change-detecting rather than folklore.
- **Definition of Done:**
  - Before returning not-found for a terminal decision, confirm the bucket is
    reachable — a HeadBucket on the not-found path only (rare in steady
    state), or a per-backend reachability cache the reconciler consults once
    per tick instead of once per object.
  - The characterization test flips to asserting the two are distinguished.
  - Revisit sooner if a FAILED-object reaper is ever added; that inverts the
    risk from "objects stop being served" to "bytes are deleted".
- **Blockers:** none. Deliberately not bundled with the classification fix —
  that fix strictly improves on a `return false` stub, and adding an S3 call
  to the reconciler's hot path is a separate decision with its own cost.

## Database

### Index candidates considered and rejected (2026-08-18 audit)

- **Status:** Deferred — decisions recorded so the audit is not repeated from
  scratch. Migrations 064–068 added the five indexes this pass judged worth
  their write cost; these are the ones it did not.
- **Reason (per candidate):**
  - **`objects (tenant_id, state) INCLUDE (size_bytes)`** — would turn the
    /stats object census and the quota reconciler's recompute into index-only
    scans. Rejected: `state` is UPDATEd on every PENDING→AVAILABLE promote and
    every delete, so the index makes the hottest write path's UPDATEs non-HOT,
    working directly against the `fillfactor` tuning migration 008 applied for
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
    the scan to one ObjectKey, so the recheck is over a page, not the table.
    Revisit if a single ObjectKey grows large enough that prefix browsing
    shows up in `pg_stat_statements`.
- **Definition of Done:** revisit each when there is production evidence —
  `pg_stat_statements` mean_exec_time, or `pg_stat_user_tables` seq_scan
  counts — rather than on inspection.
- **Blockers:** none; needs a real fleet to measure against.

### Index-usage tests are pinned to measured table sizes

- **Status:** Deferred (works today; a trap for later).
- **Reason:** `internal/integration/index_usage_test.go` proves the planner
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

## Configuration

### `ingest:` has no CUE schema block — its knobs run on Go zero values

- **Status:** Deferred (real gap, surfaced by the config-drift tests).
- **Reason:** `Config.Ingest` is a top-level block in types.go, but
  `internal/config/schema.cue` never declares it and `configs/config.yaml`
  never sets it. So nothing supplies defaults: every field falls back to its
  Go zero value. Two of those matter — `cmd/server/serve_ingest.go` passes
  `cfg.Ingest.ReaperInterval` and `cfg.Ingest.DedupTTL` straight through with
  no fallback, and `worker.RunTicker` treats `interval <= 0` as "disabled".
  An ingest deployment that doesn't spell both out therefore never reaps
  `ingested_events`, and the table grows without bound. The doc comments on
  the struct say "Default 24h" / "Default 1h", which is true of nothing.
  Recorded in `schemaGapAllowlist` (internal/config/drift_test.go) so the
  gap is explicit and a NEW one fails the build.
- **Definition of Done:**
  - `ingest:` declared in schema.cue with the defaults the struct comments
    already promise, mirroring how `worker:` / `dispatcher:` are declared.
  - The allowlist entry deleted — the test errors if a block is declared AND
    still allowlisted, so it cannot rot.
  - Either a documented `ingest:` block in configs/config.yaml, or a note
    there saying the role is opt-in and configured per-overlay.
- **Blockers:** none. Deliberately not done in the same pass as the drift
  tests: injecting defaults where zero values are live today is a behaviour
  change and belongs in its own commit, measured against a deploy that
  actually runs the ingest role.

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

### Card titles are not headings, so sections cannot be navigated

- **Status:** Open. Noticed 2026-08-21 while writing the profile e2e.
- **Reason:** `CardTitle` renders a `<div>` (`src/components/ui/Card.tsx`).
  Every section title in the console — Password, Preferences, Identity,
  Quotas — is therefore invisible to heading navigation, which is how a
  screen-reader user moves through a page. A sighted user sees structure the
  markup does not carry.
- **Definition of Done:** `CardTitle` renders a real heading, with the level
  chosen by the caller (a `level` prop defaulting to `h3`) so a card inside a
  section does not outrank the page's `h1`. Then sweep the pages: several
  currently render their own `<h2>` next to a CardTitle, which would become a
  duplicate.
- **Blockers:** none, but it touches every card in the app, so it wants its
  own change rather than riding along with a feature.

---

### Sidebar badge counts are parked and silently show nothing

- **Status:** Deferred (parked during the proto migration; never resumed).
- **Reason:** `useSidebarCounts.fetchCounts` returns a zero struct without
  calling anything — the List/Count request shapes diverged during the proto
  migration and the hook was parked rather than removed. The sidebar therefore
  renders no badges at all, which reads as "nothing to see" rather than "not
  implemented", and the hook keeps a 30s poll loop alive to produce it. It was
  found as drift: a parked comment in code with no entry here.
- **Definition of Done:** Either the counts come back — the pushdown work gave
  the List RPCs a `filter` that selects from the table, so a per-scope count is
  now expressible — or the hook, its poll and the badge slots go, and the
  sidebar stops promising a number it does not have.
- **Blockers:** none. Wants a decision on which counts are worth a query per
  navigation: objects and trash are scope-dependent, the rest are tenant-wide.

### The console holds whole tables to search them

- **Status:** Deferred (correct today, does not scale).
- **Reason:** `useBuckets`, `useBackends` and `fetchAllCollections` follow
  `nextPageToken` to the end and filter in the browser, because a filter used
  to narrow only the page it was given. That is fixed — `filter` now pushes
  into SQL — but the console still pulls everything: 859 buckets today, one
  request per 500, on every picker that offers them.
- **Definition of Done:** Pickers and list searches send the operator's query
  as a `filter` and render what comes back, keeping the paging loop only where
  a caller genuinely needs the whole set (the scope cascade's validity check).
  The 20-page ceiling stays as the backstop it is.
- **Blockers:** none technical. Wants a debounce and an empty-state that
  distinguishes "no matches" from "still typing", which is the part that is
  easy to get wrong.

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
    works against migration 008's deliberate `fillfactor` tuning.
  - Only if that is not enough: persist the reconciler's per-tenant rollup and
    read it from `/stats`, with the staleness window shown in the UI ("as of"
    already has the slot).
  - A measurement in the runbook that says when to switch: a p95 for the
    aggregate from `pg_stat_statements`, not a row-count guess. Suggested
    trigger: mean_exec_time > 1s.
- **Blockers:** none — needs a real fleet to measure against. Note the load is
  on-demand, not background: the page polls only while an operator has it open
  in a visible tab.

### Announce that bucket-scoped and per-day quota caps now reject

- **Status:** Blocked (operator action — a coding session cannot send the
  announcement).
- **Reason:** `QuotaSoftCheck` used to compare only `max_total_bytes` /
  `max_object_count`, and only against the caller's tenant row. It now also
  enforces `max_bytes_per_day` / `max_objects_per_day`, and checks the bucket
  the upload's ObjectKey resolves to. Anyone who set one of those caps while
  it was inert has a live rejection waiting: the caps were settable through
  QuotaService and MCP the whole time, and the console displayed their usage,
  so "nobody could have set one" is not a safe assumption.
- **Definition of Done:**
  - Run the over-cap query below against each environment before the rollout
    reaches it, and contact the owners of anything it returns:

    ```sql
    SELECT quota_id, tenant_id, backend_id, bucket_name,
           usage_total_bytes,  max_total_bytes,
           usage_object_count, max_object_count,
           usage_bytes_today,  max_bytes_per_day,
           usage_objects_today, max_objects_per_day
      FROM quotas
     WHERE (max_total_bytes     > 0 AND usage_total_bytes   >= max_total_bytes)
        OR (max_object_count    > 0 AND usage_object_count  >= max_object_count)
        OR (max_bytes_per_day   > 0 AND usage_bytes_today   >= max_bytes_per_day)
        OR (max_objects_per_day > 0 AND usage_objects_today >= max_objects_per_day);
    ```

    Run it as a BYPASSRLS role — `quotas` is RLS'd.
  - Release note names both changes explicitly.
- **Blockers:** none technical. Deliberately left as a human step: the rollout
  is safe on dev (where this landed) and needs a heads-up before it reaches an
  environment with real tenants.

### Platform Stats: no per-tenant breakdown for quotas / capabilities / tokens / subscriptions

- **Status:** Deferred (scope cut, deliberate).
- **Reason:** The four RLS'd censuses added alongside objects are fleet-wide
  aggregates — "12 capabilities expiring in 24h" without saying whose. That
  matches how the inventory cards (backends, buckets) already read, and the
  per-tenant table on the page is object-shaped: bolting four more dimensions
  onto it would make the busiest surface on the page unreadable. The queries
  themselves would be trivial to group by tenant_id; the UI is the hard part.
- **Definition of Done:**
  - Either a per-tenant drill-down (click a card → filtered table) or a second
    table keyed by tenant with a column group per census.
  - The ops payload carries per-tenant rows for whichever censuses the UI
    actually drills into — not all four speculatively, since each multiplies
    the payload by the tenant count.
- **Blockers:** none. Wants a design pass on the page first — it already
  carries five cards plus a wide table.

### Platform Stats: per-tenant table caps at 200 rows

- **Status:** Deferred (bounded payload beats a complete one, for now).
- **Reason:** `platformstats.maxTenantRows` trims the census to the 200 busiest
  tenants and reports the omitted count; the rollup row still totals every
  tenant, so the aggregate is never wrong — only the drill-down is partial. A
  paginated or sortable table is the real answer, but it needs a page token on
  the RPC and column-sort state in the UI, which is a bigger surface than the
  first cut of the page justified.
- **Definition of Done:**
  - The RPC takes a page token / sort key, or the console filters server-side
    by tenant slug.
  - The "N smaller tenant(s) omitted" note becomes a link that pages rather
    than a dead end.
- **Blockers:** none.


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

### Backend edit dialog: advanced fields (SSE / events / cedar_policy)

- **Status:** Deferred (metadata edit landed; advanced fields parked).
- **Reason:** The backend detail page's Edit dialog
  (`frontend/src/app/storage-backends/[backendId]/BackendActions.tsx`) wires
  `UpdateBackend` for the mutable metadata fields
  (`display_name`, `endpoint`, `public_endpoint`, `region`,
  `force_path_style`). The server's update mask
  (`backend/internal/store/postgres/adapters/admin_backend.go` `Update`) also
  accepts `sse`, `events`, and `cedar_policy`, but those are nested
  sub-messages (SSE config, EventSourceConfig, a Cedar policy body) that each
  need their own editor UX; the 80% operator edit is the flat metadata, so the
  advanced fields were left out to keep the dialog focused.
- **Definition of Done:** Extend the Edit dialog (or add a separate "Advanced"
  section/tab) with editors for `sse` (type + key id), `events` (enabled /
  target / queue URL / poll interval), and `cedar_policy` (policy textarea),
  append the matching mask paths to `UPDATE_BACKEND_MASK` in
  `frontend/src/hooks/useBackends.ts`, and cover each with a vitest case
  asserting the mask + sub-message shape. Delete this entry when done.
- **Blockers:** none — needs a UX call on whether advanced config belongs in
  the same dialog or a dedicated panel.

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
- **Blockers:** a Next.js release that makes the `output: standalone` server
  execute a `proxy` function (recheck at each upgrade — see the rationale block
  atop `frontend/src/middleware.ts`).
- **Recheck log:**
  - **2026-07-21 — 16.2.10 (latest stable): STILL BLOCKED.** Bumped
    `next` + `eslint-config-next` 16.2.6 → 16.2.10, renamed to `proxy.ts` /
    `export function proxy`, ran an `output: standalone` build. A control build
    of the same tree under `middleware.ts` populated the manifest
    (`middleware: ["/"]`, `sortedMiddleware: ["/"]`, real matcher regexp);
    the `proxy.ts` build left it empty (`middleware: {}`, `sortedMiddleware:
    []`, `functions: {}`) — same version, same build, only the convention
    changed. New datum: `proxy` is now **Node-runtime-only by design** — adding
    `export const runtime = "edge"` hard-errors the build (`Route segment
    config is not allowed in Proxy file … Proxy always runs on Node.js
    runtime`), so the edge path that middleware relies on is not reachable from
    `proxy`. The fix must come from Next running the Node-runtime proxy in the
    standalone server, not from a config workaround on our side. Reverted the
    bump + rename; `middleware.ts` stays.

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
  `objectKeyNameParts` parses ONLY the C-shape with an INLINE `assertJWTTenant`
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
- **Shipped — Phase 3 cross-backend integration test:** a two-backend
  integration test (`internal/integration/storage_migration_crossbackend_test.go`)
  stands up two physically distinct MinIO backends and drives the router's
  cross-backend copy (`ObjectRouter.CopyObject` → `GetStream` piped into the
  destination's multipart writer) for a 12 MiB object, asserting size + a
  byte-for-byte sha256 match on the destination and that the source is retained.
  This closes the "cross-backend proven end-to-end" gap the unit tests couldn't.
- **Definition of Done (remaining — Phase 3 hardening):**
  - Live cross-backend run on the dev cluster (infra-dependent — needs the
    `secondary`/SeaweedFS backend back online; the integration test above is the
    code-side proof).
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
  grant (`garage key allow --create-bucket <key>`) or the ADR-0011
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

### `dev-bootstrap.sh` is repaired but unexercised

- **Status:** Open. Surfaced 2026-08-21.
- **Reason:** the script had been calling `paladin.v1.TenantService` — a proto
  package that has not existed for some time — and sending UpdateTenant's
  fields at the top level after they moved under `tenant`. It could not have
  worked, which means nobody ran it and nothing noticed. It has been ported to
  the current contract (AIP-122 create shape, `SetCollectionPolicy`), but only
  `bash -n` has been run against it; no live backend has executed it.
- **Definition of Done:** run it against a fresh dev stack and confirm the UI
  can list tenants, buckets and collections afterwards. Then decide whether it
  is worth a smoke job — a bootstrap script that silently rots is worse than
  no bootstrap script, because it is the first thing a new contributor runs.
- **Blockers:** none; needs a running stack.

---

### `verify-all` does not run the integration suites — one rotted unseen

- **Status:** Open. Surfaced 2026-08-21.
- **Reason:** `task verify-all` runs unit tests, lint and the frontend build.
  Both integration suites sit behind the `integration` build tag and run only
  in `.github/workflows/integration.yml`. With CI blocked on the account-wide
  Actions spending limit, nothing ran them for the length of a large refactor
  — and `backend/tests/integration/` (23 files) stopped compiling entirely
  without anyone noticing. When it was fixed, 45 tests failed and four of the
  failures were production bugs, not stale fixtures: a silent no-op on default
  binding, broken ingest dedup, a charges ledger that could never insert, and
  a dispatcher that treated transient DB errors as permanent failures.
- **Definition of Done:** either (a) `verify-all` gains a cheap
  compile-only step — `go vet -tags=integration ./...` — so a suite that stops
  building fails the local gate in seconds, or (b) a documented, enforced rule
  that the integration task runs before merge. (a) is the cheaper half and
  catches the failure mode that actually occurred; it does not catch a suite
  that compiles but fails, which is what CI is for.
- **Blockers:** the full suites take ~11 minutes and need Docker, so putting
  them in `verify-all` outright would make the local gate unusable. That is
  the reason they are not there, and it is still a good reason.

---

### The Go admin e2e suite has no gate at all — it had stopped compiling

- **Status:** Open (the suite is repaired and passing; the gate is what is
  missing). Surfaced 2026-08-27.
- **Reason:** `backend/tests/e2e/admin_api_test.go` sits behind
  `//go:build e2e`, and nothing anywhere runs it — `grep -rn "tags=e2e"` over
  `.github/` and every Taskfile returns nothing. Not a workflow, not
  `verify-all`, not even a compile check. This is the failure mode described
  one entry up for the integration suites, and it produced the same outcome:
  the file had drifted out of the API it tests and no longer built.
  `DeleteTenantRequest.Force` and `Bucket.bucket_name` had been removed from
  the proto (both fields are `reserved` now), so `go test -tags=e2e` failed at
  compile with five errors. Underneath that sat two more drifts a compile
  check would NOT have caught: every `Create*` is rejected without an
  `Idempotency-Key` header, and delete on all four entities is OCC-guarded —
  collections and buckets take `skip_version_check`, tenants and backends have
  no bypass at all and need the current `resource_version` read first. A
  tenant also takes two calls to remove now (`DeleteTenant` trashes,
  `PurgeTenant` removes), where `force` used to do both.
- **Repaired 2026-08-27:** 13/13 subtests pass against the Playwright suite's
  compose stack, and teardown drains every row it creates:

  ```
  PALADIN_ADMIN_URL=http://localhost:8090 \
  PALADIN_JWT_SECRET=dev-secret-change-me-32-bytes-min \
  PALADIN_JWT_ISSUER=paladin-dev \
    go test -tags=e2e -count=1 ./tests/e2e/...
  ```

- **Definition of Done:**
  - A compile-only gate — `go vet -tags=e2e ./...` in `verify-all`, next to
    the `-tags=integration` step the sibling entry asks for. Seconds of local
    runtime, and it catches exactly the failure that happened here.
  - A run gate. `.github/workflows/e2e.yml` already builds both images and
    boots `frontend/tests/e2e/docker-compose.test.yaml`, which publishes the
    admin plane on `:8090` — the one thing this suite needs. A step after the
    Playwright run, with the three env vars above, is close to free; the
    alternative is admitting the suite is manual and saying so in its header
    comment instead of leaving a run recipe that reads like it is wired up.
- **Blockers:** none. The run gate depends on the e2e workflow executing on
  Actions at all, which is the *Playwright e2e suite* entry's remaining item;
  the compile gate does not depend on anything.

---

### Playwright e2e suite wired into CI

- **Status:** Workflow AUTHORED (2026-07-02), storage reworked into the
  compose stack and confirmed green locally (2026-08-19) — the Actions run
  and the required-check flip remain.
- **Shipped:** `.github/workflows/e2e.yml` — builds both images from the
  deploy Dockerfiles (`:latest` tags the compose file references), installs
  pnpm + Chromium, pre-pulls the stack's third-party images, runs
  `pnpm run test:e2e` (Playwright's webServer boots the compose stack
  itself), uploads the report artifact + stack logs on failure. Suite is
  currently 18/18 locally (incl. tenant switching).
- **2026-08-19 update:** storage moved INTO
  `frontend/tests/e2e/docker-compose.test.yaml` as a `minio` +
  `minio-setup` pair. The workflow no longer starts its own MinIO on the
  runner host, and the docker0-gateway address and `PALADIN_E2E_S3_*` env
  block are gone with it — CI now runs the same stack a contributor runs.
  This also removed the suite's last dependency on the private `gitops`
  repository (previously a cluster-shared Garage reached by
  `kubectl port-forward`, with credentials extracted from a K8s Secret),
  which made the suite unrunnable for anyone outside that cluster.
  **Confirmed 2026-08-19:** first `compose up` against the MinIO services
  ran 18/18 green in 1.5 min on Docker 29.4.0 (darwin/arm64), from the two
  locally built `:latest` images and nothing else — no cluster, no
  `kubectl port-forward`, no `PALADIN_E2E_S3_*` overrides. `minio` and
  `minio-setup` came up in the declared order, the bucket was created, and
  the browser's presigned PUT to `localhost:9000` passed SigV4 — which is
  the assertion that the two-endpoint split (`endpoint` vs
  `public_endpoint`) exists to make.
- **Definition of Done (remaining):**
  - First green run on Actions. Still blocked 2026-08-19: the repository is
    private and its Actions runs fail at startup with zero steps executed
    (see *Branch protection* for the confirmation). Publishing the
    repository is what removes this.
  - Flip to a required check alongside `test` / `security` (see the
    *Branch protection* entry).
- **Blockers:** none remaining that are outside the maintainer's control.


### Branch protection on `main` and `develop` — require status checks

- **Status:** Partially done — deletion/force-push/linear-history applied
  2026-06-28 and now also reproducible-as-code (2026-07-26); required status
  checks still deferred.
- **2026-07-26 update:** the non-required settings are now version-controlled in
  [`.github/scripts/apply-repo-settings.sh`](.github/scripts/apply-repo-settings.sh)
  — it sets `delete_branch_on_merge=false` and creates a **ruleset** (not classic
  branch protection) forbidding deletion of `main`/`develop`. Ruleset over
  classic protection on purpose: classic branch protection needs a **paid** plan
  for a private repo, so if the plan lapsed with the billing failure below the
  2026-06-28 rules would have silently dropped — whereas rulesets work on free
  private repos and layer additively (they never clobber any classic protection
  still in place). Must be run by the repo owner (`gh auth login`), since a
  sandboxed session has no GitHub auth. Separately, CI rate-limit flakiness (the
  "fails, passes on retry" class) was hardened this commit: optional Docker Hub
  auth (`.github/actions/dockerhub-login`) across the image-pulling jobs, the
  Trivy DB pointed at the ECR Public mirror, ryuk disabled for testcontainers,
  and `golangci-lint-action` for a cached/retried linter install.
- **Reason:** The non-blocking half landed via the API on both branches:
  force-pushes disabled, branch deletion blocked, linear history required,
  `enforce_admins` on. The **required status checks** half
  (`backend`, `frontend`, `gitleaks`, `trivy-fs`) is intentionally still OFF
  because every Actions run currently fails at startup (0 steps executed) —
  the signature of an exhausted private-repo Actions minutes / spending
  limit. Requiring red checks would block all merges and direct pushes.
- **Confirmed 2026-08-19** against the live repository: the five most recent
  runs on `claude/open-source-prep-85tasf` (Test, E2E, Integration, Security,
  Capability module) all end `failure` after 5–7 seconds, and
  `…/actions/runs/<id>/jobs` reports each job with an EMPTY `steps` array —
  the jobs never start, so this is not a code failure and no log exists to
  read. The repository is still `"private": true`, so the free-minutes
  argument below has not taken effect yet: **publishing the repository is
  the unblock, and until then nothing on Actions can go green.** Everything
  the pipeline would check is green locally (`task verify-all`, integration,
  e2e 18/18), so the gap is quota, not correctness.
- **The quota is not this repository's doing.** Billing usage for August 2026
  (`/users/oleg-tkachuk/settings/billing/usage`) puts the account at 4734.7
  Actions Linux minutes, of which **`paladin` accounts for 109.0 —
  2.3%**. `another-project` (2781.7) and `gitops` (1752.0) are 96% between
  them. The spending limit is account-wide, so this repository is blocked by
  neighbours rather than by anything it runs. Do NOT come here to trim
  Paladin's CI: 109 minutes a month is already nothing, and halving it would
  change the date this unblocks by zero days.
  The money line is gross $28.41, free-tier discount $18.41 (= 3068 minutes,
  i.e. the Pro plan's 3000/month), net **$10.00**. July was the same shape:
  4674.7 minutes, net exactly $10.00. Two consecutive months landing on the
  same round number is a spending ceiling being hit, not usage that happens
  to match — the limit itself is not readable via the API (the budgets
  endpoints 404 and the old billing ones are 410 Gone), so this is inferred
  from the figures rather than quoted.
  Options, in the order they actually help: publish the repository (public
  repos consume no quota at all, so this stops recurring and is already the
  plan); raise the spending limit (unblocks every private repo at once, and
  costs); or wait for the 1st (free minutes reset, but the ceiling was
  reached in both July and August, so it returns).
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
- **2026-08-19 update:** `apply-repo-settings.sh` now composes the ruleset
  with `jq` and takes `REQUIRE_CHECKS=1` to add the four contexts, so
  turning them on is one env var rather than a hand-written API call. The
  script also enables private vulnerability reporting (SECURITY.md links
  to the advisory form, which 404s without it) and Dependabot alerts. It
  still leaves visibility alone — flipping a repository public is a
  one-way door and belongs to a human.
- **Blockers:** GitHub Actions billing — every run failed with `steps=0`
  and "The job was not started because recent account payments have
  failed or your spending limit needs to be increased" (re-verified
  2026-06-30). **Publishing the repository dissolves this**: Actions
  minutes are free on public repositories, so the account-level spending
  limit stops applying. What remains after that is not a blocker but a
  precondition — confirm a run actually goes green on each of the four
  contexts before requiring them, because `enforce_admins` is on and a
  required check that never reports blocks every merge and direct push on
  both branches.


### `frontend/tasks/docker.task.yaml` defaults an overlay path into a private repo

- **Status:** Deferred — considered and consciously kept.
- **Reason:** `OVERLAY` defaults to
  `../../gitops/deploy/argocd-apps/applications/overlays/local/values/paladin/…`,
  a sibling repository that is not public. For anyone else that path does
  not exist, so the task silently falls back or fails depending on the
  code path. The e2e and compose stacks no longer depend on `gitops`;
  this deploy helper is the last coupling, and it only affects the
  maintainer's own cluster deploys.
- **Definition of Done:** either the default becomes empty (overlay opt-in
  via `OVERLAY=…`), or the task documents that it is maintainer-specific
  and exits cleanly when the path is absent.
- **Blockers:** none. Left as-is deliberately so the maintainer's deploy
  loop keeps working; revisit if an outside contributor ever needs the
  Kubernetes deploy tasks.

---

## Documentation

## Capability module

### Smoke test has no CI home — runs only on demand

- **Status:** Deferred
- **Reason:** `TestSmokeStackReady` (tests/integration/smoke_test.go) probes a
  live docker-compose stack on localhost — it is not a testcontainers test.
  Wiring the integration suite into CI made it fail on every run, because no
  job brings the compose stack up. It now skips unless `PALADIN_SMOKE=1`, which
  keeps the gate green but means the smoke test gates nothing: the "the stack
  composes and every plane binds" claim it exists to check is unverified in
  CI. The frontend e2e workflow brings up a DIFFERENT compose file
  (`frontend/tests/e2e/docker-compose.test.yaml`), so it is not covered there
  either.
- **Definition of Done:**
  - A CI job (or a step in an existing one) brings up
    `backend/deploy/docker-compose.yaml`, waits for health, then runs the
    smoke test with `PALADIN_SMOKE=1`, and tears the stack down.
  - The 60s in-test timeout is reconciled with the job-level timeout so a
    stack that never comes up fails fast with a clear message rather than
    hanging.
- **Blockers:** none. Needs a decision on whether it lives in the integration
  workflow (adds a compose bring-up to a testcontainers job — mixed concerns)
  or its own smoke workflow.

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
---
## BatchDeleteObjects cannot delete permanently

- **Status:** Deferred (the dangerous half is fixed; the feature is not).
- **Reason:** The request has a `permanent` flag and the executor always
  soft-deletes. It used to accept `permanent=true` and soft-delete anyway,
  which told a caller its erasure had succeeded while the objects sat in the
  trash — the worst possible answer for a deletion-on-request workflow. It now
  returns Unimplemented for that flag, so the refusal is honest, but the
  capability is still missing: callers must fall back to per-object
  DeleteObject(permanent=true).
- **Definition of Done:** The executor performs hard deletion (storage bytes +
  row) when asked, honouring object-lock rules per object exactly as
  DeleteObject does, and the Unimplemented guard in the shim is removed.
- **Blockers:** Hard delete currently lives in a TTL-driven housekeeping job
  rather than an on-demand path; batch would need that logic factored out.


---
## List filters push down only the conjuncts SQL can express

- **Status:** Deferred (the remainder needs per-schema work, not a rule).
- **Reason:** The filterable list RPCs now extract the SQL-expressible subset
  of the caller's CEL (`cel.ExtractPushdown`) and hand it to the query, so
  `filter` selects from the table rather than from whichever page the cursor
  landed on. What the walk understands is the top-level `&&` chain of string
  equality, `startsWith`, `contains`, and booleans over columns the query
  carries. Everything else — disjunctions, timestamp comparisons, `labels[…]`,
  functions — still reaches only the in-memory pass, which means a filter made
  entirely of those reads the whole table one page at a time. That is correct
  (paging continues, no row is dropped) and slow.
  Two paths are narrower still and worth naming: `ListCollections` has a
  hand-written branch for the (backend, bucket) browser that takes no hints at
  all, and `operations.state` is deliberately not pushed from a filter because
  the column is an enum and casting an arbitrary literal to it makes Postgres
  reject the whole query rather than return no rows.
- **Definition of Done:** Either the walk covers the rest of the CEL surface
  each schema exposes — timestamp ranges are the obvious next one, and
  `auditpushdown.go` already does them for its own schema — or the schemas
  stop exposing what no query can answer.
- **Blockers:** none. Deliberately not solved by rejecting un-pushable filters
  with InvalidArgument: that would make a legal CEL expression an error
  because of an implementation detail of one storage engine, and the shape the
  object and audit paths established is narrow-only for exactly that reason.

---

## Two write paths are covered only up to their promote tail

- **Status:** Open (narrowed 2026-08-27). Was: thirteen RPC handlers at 0.0%.
- **Reason:** The thirteen now have behavioural tests and none is at zero —
  `./internal/api/...` went 28.0% → 32.9%. Eleven are covered end to end.
  `CompleteObject` (59.1%) and `CopyObject` (53.3%) are not: both finish by
  promoting through a concrete `*statemachine.Transitioner` rather than an
  interface, so the tail — promote, the in-transaction
  `paladin.object.uploaded` dispatch, the version record, the quota touch —
  cannot be reached without a database. What is covered is everything before
  it: the argument guards, the state guards, and the authorization Resource
  each hands the engine, including the one that matters most on a copy (the
  Resource is the DESTINATION, not the source being read).
- **Definition of Done:** either the promote seam becomes an interface the
  handler can be handed a fake for, or these two gain integration tests that
  drive the real transitioner against Postgres — `tests/integration/` already
  has the harness. The second is less invasive and tests more; the first makes
  the handler unit-testable for everything that follows.
- **Blockers:** none. It is a choice about where the seam belongs.

---

## The RPC surface gate skips in CI, so it gates nothing there

- **Status:** Open. Surfaced 2026-08-27.
- **Reason:** `tests/integration/rpc_surface_test.go` is the only thing that
  covers all 142 RPC declarations at once, and it reaches them over HTTP:
  `127.0.0.1:8090` / `:8080` / `:8085`. When no stack answers it calls
  `t.Skip`. `.github/workflows/integration.yml` starts a testcontainers
  Postgres and nothing else — no `docker compose`, no `paladin-core` — so in CI
  it has always skipped, silently, and a skipped gate reads exactly like a
  passing one. It runs locally only for someone who happens to have the e2e
  compose stack up, which is how it was found.
  The test anticipated this: `PALADIN_RPC_SURFACE=1` turns the skip into a
  failure. Nothing sets it.
- **Definition of Done:** the integration job brings up
  `frontend/tests/e2e/docker-compose.test.yaml` (it already publishes all
  three planes on those ports) and sets `PALADIN_RPC_SURFACE=1`, so an
  unreachable stack fails the job instead of quietly excusing it.
- **Blockers:** depends on the integration workflow running on Actions at all
  — see *Branch protection*.

---

## Move the api/v0.4.0 baseline for the token resource-name change

- **Status:** Blocked (operator action — moving a published tag is a release
  decision, not a coding one). Surfaced 2026-08-28.
- **Reason:** `APITokenService` now addresses tokens by resource name, which is
  a deliberate break. docs/upgrading.md carries the entry, which is step 3 of
  the procedure in that file. Step 4 is not mine: it force-moves the published
  `api/v0.4.0` and `api/latest` tags, which changes what those tags mean for
  anyone who pinned them.
  Until it is done, the `buf breaking` job in `.github/workflows/test.yml`
  fails against the old baseline — correctly, since the contract did change.
- **Definition of Done:**

  ```
  git tag -f -a api/v0.4.0 -m "api tokens addressed by resource name"
  git tag -f api/latest
  git push --force origin api/v0.4.0 api/latest
  ```

  then bump `breaking_against` in the workflow if the tag name changes.
- **Blockers:** the maintainer's call on whether this rides the existing
  v0.4.0 baseline or opens a new one.

---

## Terminal tenant events are observed by query, not by subscription

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

## The BFF's rotation bookkeeping is per-process

- **Status:** Deferred (single-replica assumption, already documented).
- **Reason:** Two maps in `bff.ts` make concurrent token work safe: the dedup
  that collapses identical rotations, and the successor index that lets an
  ExchangeAudience follow a rotation that consumed its token
  (`currentRefreshToken`). Both live in process memory. With one console
  replica that is exactly right; with two, a request routed to the replica
  that did not perform the rotation sees nothing to follow and fails the same
  way it did before — the operator is logged out by a race.
- **Definition of Done:** Either the deployment pins the console to one
  replica and says so, or the bookkeeping moves to a shared store (Redis) so
  any replica can follow a chain another one advanced.
- **Blockers:** none technical. It is a decision about topology, not code:
  bff.ts has carried the single-instance note since the dedup was written, and
  this is the second mechanism to inherit it.

---

## Streaming RPCs are charged one rate-limit token at open

- **Status:** Deferred (matches today's streams; revisit when one is chatty).
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

---
## A reclaimed operation records a count, not which items landed

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

## `container_*` metrics do not exist on this cluster, and the scrape says otherwise

- **Status:** Blocked (OrbStack's kubelet, not our configuration).
- **Reason:** Alloy scrapes the node's cAdvisor endpoint and reports success —
  `up{job="cadvisor"} = 1` — while the endpoint returns 25 lines of
  `machine_*` and no `container_*` series at all. So there is no per-container
  CPU, memory or throttling data in VictoriaMetrics, and any panel or alert
  built on `container_cpu_*` would render empty while looking correctly
  configured. This was noticed while trying to establish whether a TLS
  handshake timeout came from CPU starvation in the api pod: the question
  could not be answered, because the data does not exist.
- **Definition of Done:** Either a source of per-container resource metrics
  that works here (cadvisor as a DaemonSet, or the metrics-server API), or a
  note in the observability docs that resource-level questions cannot be
  answered on OrbStack — so the next person does not spend the time twice.
- **Blockers:** OrbStack. The same scrape returns full `container_*` data on a
  normal kubelet, so nothing in the Alloy config needs changing for a real
  cluster.

---

## Something inside the api pod speaks plain HTTP to its own TLS port

- **Status:** Deferred (cosmetic today; the client was not identified).
- **Reason:** The api pod logs `http: TLS handshake error from 127.0.0.1:
  client sent an HTTP request to an HTTPS server` — 14 of them inside a
  21-second burst, 18 minutes into the pod's life, during an e2e run. No other
  pod logs it. It is not the kubelet probes: liveness, readiness and startup on
  api and admin all carry `scheme: HTTPS`, their periods (20s / 10s / 5s) do
  not fit a 14-in-21-seconds burst, and a probe would not come from 127.0.0.1.
  Whatever the client is, it is inside the pod and it is wrong about the
  scheme. Harmless so far — the connections fail and something evidently
  retries or ignores them — but it is noise in exactly the log an operator
  greps when chasing a real handshake failure, which is how a genuine one
  (the console BFF's, from a different IP) nearly got lost in the count.
  The burst has not recurred since — two full e2e runs on later builds logged
  none — so it cannot currently be caught in the act.
- **Definition of Done:** The client is identified and either corrected or
  documented. The message now carries `listen_addr`, so the next occurrence
  says whether it arrived on data (8080) or iam (8085); that halves the search
  and makes the MCP loader's `http://localhost:8085` default a checkable
  suspect rather than a guess. `ss -tnp` inside the pod during a burst would
  finish the job.
- **Blockers:** it stopped happening. Waiting for a recurrence with the tag
  attached beats guessing at a client that may already be gone.
