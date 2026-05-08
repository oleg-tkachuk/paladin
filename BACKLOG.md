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

### Capability caveats — Budget + Source-IP CIDR enforcement

- **Status:** Aspirational
- **Reason:** Op + ResourcePrefixes / ResourceURIs caveats now gate
  every read/write/list on ObjectService / MultipartUploadService /
  PresignService / BatchService / ObjectTagService (Phase 1). The
  remaining caveat axes — `MaxBudgetUSD` and `SourceIPCIDR` — are
  declared on Capability but not yet enforced anywhere.
- **Definition of Done:**
  - Budget tracker: per-capability USD spend on object PUT (storage
    bytes × tiered rate), GetObject (egress estimate), Embed /
    Sampling LLM calls (proxy-reported cost). Decrement on each op;
    surface `ErrBudgetExceeded` from the next call once exhausted.
    Lives in `internal/auth` with a Postgres-backed counter table.
  - Source-IP CIDR caveat enforced in the interceptor (before any
    handler runs) — connect.RealIP + cap.Caveats.SourceIPCIDR
    membership check; deny with CodePermissionDenied otherwise.
  - The `capability.OpShare` path lights up when the MCP `share` tool
    routes to `CapabilityService.Delegate` — the caller's capability
    must include OpShare for the delegation request to succeed.
- **Blockers:** none. Independent of the Op/Resource caveats which
  are wired.

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

### CEL filter SQL pushdown for AuditLog list/export

- **Status:** Deferred
- **Reason:** [internal/api/admin/v1/audith/handler.go](internal/api/admin/v1/audith/handler.go)
  applies the CEL predicate **after** SQL pagination — works for v1
  (page size 1000), but selective filters waste 99% of fetched rows
  on large audit logs.
- **Definition of Done:**
  - CEL → partial SQL translator for the common predicates
    (action prefix, actor_subject equality, time-range).
  - Fallback to in-memory CEL for the residual.
  - Benchmark proving ≥10× speedup on a representative filter.
- **Blockers:** none.

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

### UserSettings — buf-generated connect-rpc swap

- **Status:** Deferred
- **Reason:** The feature is fully reachable today via a hand-written
  JSON shim at [internal/api/connectshim/iam/user_settings_server.go](internal/api/connectshim/iam/user_settings_server.go).
  The proto definition lives at
  [proto/paladin/iam/v1/user_settings_service.proto](proto/paladin/iam/v1/user_settings_service.proto)
  but `buf generate` hasn't been run for it — the generated `*.pb.go`
  + `*_connect.go` artifacts are missing. The shim mounts at the same
  path namespace (`/paladin.iam.v1.UserSettingsService/{Method}`) so
  swap-in is mechanical once codegen runs.
- **Definition of Done:**
  - `buf generate` produces `user_settings_service.pb.go` +
    `user_settings_service.connect.go`.
  - `connectiam.UserSettingsServer` rewritten to satisfy the generated
    service interface (replace the JSON shim).
  - `cmd/server/root.go` swaps `connectiam.RegisterUserSettings` for
    the `paladiniamv1connect.NewUserSettingsServiceHandler` form.
- **Blockers:** none — mechanical once codegen runs.

### Tenant slug rollout — Phase 3 (resource-name slug acceptance)

- **Status:** Deferred
- **Reason:** Phase 1+2 landed: `tenants.slug` column, slug-aware
  `cedar.Principal/Resource`, default-policy templating on slug, JWT
  `tenant_slug` claim minted by both `authh` and `apikeyh` (via
  `WithTenantSlugLookup` + `wire.tenantSlugLookup`), MCP/proto comment
  hints rewritten to `tenants/{tenant_id_or_slug}`, and
  `apiutil.ParseTenantNameRef` is available alongside the legacy parser.
  What remains is the actual handler-side acceptance: most RPCs still
  call `apiutil.ParseTenantName` (UUID-only) instead of `ParseTenantNameRef`
  with slug-resolution.
- **Definition of Done:**
  - All `apiutil.ParseTenantName` callers migrated to
    `ParseTenantNameRef` + slug-resolution via
    `TenantRepo.GetTenantBySlug` (already wired in sqlc).
  - Admin RPC `RenameTenantSlug` (with policy rewrite — replace old slug
    in `tenants.inherited_cedar_policy` + per-objectKey policies) plus
    audit-log emission.
- **Blockers:** none — incremental work; tracked here so handlers don't
  drift apart as they migrate.

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

### Event dispatcher: Kafka / SQS sinks

- **Status:** Deferred
- **Reason:** [event_dispatcher.go:120](internal/worker/event_dispatcher.go)
  returns `"sink %q delivery not yet wired (slice 8)"` for `kafka` /
  `sqs`. Only `http` works today.
- **Definition of Done:**
  - SQS sink with batch `SendMessageBatch`.
  - Kafka sink with `franz-go` and per-tenant topic prefix.
  - Sink-config schema validation at `Create`/`Update` time.
- **Blockers:** Kafka client library decision (sarama vs franz-go).

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
