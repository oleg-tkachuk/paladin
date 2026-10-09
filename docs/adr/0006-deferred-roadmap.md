# ADR-0006: Deferred roadmap

- **Status:** Superseded by [`BACKLOG.md`](../../BACKLOG.md),
  [ADR-0009](0009-oauth-authorization-server.md) and
  [ADR-0015](0015-per-tenant-bucket-layout.md) (2026-10-06). Most of what it
  lists has shipped — the event sinks, JetStream ingest, the OAuth
  authorization server, per-tenant buckets; BACKLOG.md is the deferred-work
  register. Original status: Accepted (living index).
- **Context:** Beyond the production-readiness fixes and the items now
  promoted to ADRs 0001–0005, the remaining backlog is feature/roadmap
  work that is deliberately deferred. This ADR records *that they are
  deferred and why a direction is not yet chosen*; the detailed
  Status/Reason/DoD/Blockers for each lives in
  [`../../BACKLOG.md`](../../BACKLOG.md).

## Decision

> **Amendment 2026-06-30 — IAM direction settled.** Paladin is an
> engineer-operated control plane that integrates with other services and
> exposes an API for bucket access/management. Human authn stays in Paladin's
> own IAM (local users + HS256, plus Paladin-IAM-as-AS per
> [ADR-0009](0009-oauth-authorization-server.md)); service-to-service stays
> on `api_keys` + capabilities. **"Phase 5b.1 — drop user-authn IAM, accept
> OIDC"** and **"Federated IdP via JWKS"** are **withdrawn** from this
> roadmap — no external IdP is needed now. Both revert to a future swap only
> on a concrete customer SSO mandate (the OAuth RS/AS halves are
> IdP-agnostic, so the swap stays cheap). Struck from the lists below.

The following stay in BACKLOG as scoped-but-unscheduled; none blocks
production readiness, and each needs a product/infra decision or a
customer ask before it earns an implementation slot:

### Single-binary multi-mode / role splits
Extract `event-dispatcher`, `scheduler`, `indexer`/`embedder`,
`billing-aggregator`, `auth-server` (OAuth AS), and `realtime`
(SSE/WebSocket) from the monolith; streaming RPCs through the inline
transport. **Trigger:** scale/isolation needs that don't exist yet at
current load.

### Event dispatcher sinks & semantics
NATS (first non-HTTP sink), Kafka, RabbitMQ, SQS sinks; NATS NKey/JWT
auth; CloudEvents 1.0 envelope; producer-wiring integration tests;
JetStream ingest upgrade. **Trigger:** customer ask + a chosen client
library per sink.

### Agentic / MCP
OAuth 2.0 authorization-code flow for MCP clients (desktop agents,
IDE plugins); live session enumeration on the streamable-HTTP transport.

### Storage & data
Real `StorageReplicator`; per-tenant S3 bucket layout; cross-region DB
replication; WAL archiving + PITR runbook; `audit_log` /
`idempotency_keys` partitioning; per-table autovacuum tuning.

### Security & platform
KMS-wrapped capability signing key; NetworkPolicies
per role; SealedSecrets for prod clusters; audit-log encryption at rest;
RLS coverage of the remaining cross-tenant tables; per-row Cedar filtering
in ListObjects; `/system/health.json` auth (coupled with the frontend
aggregator).

### CI / delivery
golangci-lint CI gate (codebase must pass the config first); Playwright
e2e wired into CI (after flake sign-off); image vulnerability scanning;
branch protection (was blocked on GitHub billing); Node 25 → LTS
decision; base-image digest pinning.

### UI / admin & API ergonomics
Vitest DOM/component tests (jsdom + RTL) on top of the unit scaffold; Zod
runtime validation for `JSON.parse` sites; one data-hook error contract;
oversized-component refactor; re-promote the react-hooks v6 rules;
connectshim proto↔struct converter codegen; resource-name resolution
RPCs; bucket sub-tabs; tag filters.

## Consequences

- BACKLOG remains the single source of truth for the detail; this ADR is
  the durable "we looked and chose to wait" record so the deferral is a
  decision, not an oversight.
- An item graduates here → its own ADR (and an implementation PR) when a
  direction is picked.
