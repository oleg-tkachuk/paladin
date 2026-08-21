# Architecture Decision Records

Each ADR captures one significant, hard-to-reverse decision: the context,
the choice, and its consequences. Numbered sequentially; never delete or
renumber a landed ADR — supersede it with a new one and link back.

Status vocabulary: **Accepted** (decided + implemented), **Proposed**
(decided in principle, implementation pending — carries the plan),
**Superseded by NNNN**, **Deprecated**.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-otel-observability-baseline.md) | OpenTelemetry observability baseline | Accepted |
| [0002](0002-api-error-connect-mapping.md) | Centralized error → Connect-code mapping | Accepted |
| [0003](0003-transactional-outbox.md) | Transactional event outbox | Accepted |
| [0004](0004-table-backed-audit-outbox.md) | Crash-durable (table-backed) audit outbox | Accepted |
| [0005](0005-cnpg-ha-ownership.md) | CNPG Postgres HA ownership & verify-full TLS | Accepted |
| [0006](0006-deferred-roadmap.md) | Deferred roadmap (single-binary planes, event sinks, …) | Accepted |
| [0007](0007-postgres-connection-headroom.md) | Postgres connection headroom & pooling ownership | Accepted |
| [0008](0008-mcp-oauth-resource-server.md) | MCP server as an OAuth 2.1 Resource Server | Accepted |
| [0009](0009-oauth-authorization-server.md) | OAuth 2.1 Authorization Server (IAM-as-AS) | Accepted |
| [0010](0010-capability-as-establishing-credential.md) | A capability may establish identity on the data plane | Accepted |
| [0011](0011-narrow-role-for-tenant-provisioning.md) | A narrow role for tenant provisioning | Accepted |
| [0012](0012-machine-principals-may-delete-their-own-objects.md) | Cedar knows the credential kind; machines may delete their own objects | Accepted |
| [0013](0013-object-lock-retention-and-legal-hold.md) | Object Lock — retention a mode of Paladin cannot lift | Accepted |

## Backend-scoped ADRs

A second set lives in [`../../backend/docs/adr/`](../../backend/docs/adr/),
covering decisions internal to the backend's schema and API surface rather
than the platform as a whole. The two sets number independently, so **0010
through 0012 exist in both** — cite them as "ADR-NNNN" for this directory
and "backend ADR-NNNN" for the other.

| ADR | Title | Status |
|-----|-------|--------|
| [backend 0010](../../backend/docs/adr/0010-canonical-resource-names.md) | Canonical resource names (A + B + C) | Accepted |
| [backend 0011](../../backend/docs/adr/0011-per-tenant-bucket-layout.md) | Per-tenant bucket layout | Accepted |
| [backend 0012](../../backend/docs/adr/0012-cedar-tenant-membership-isolation.md) | Cedar tenant-membership isolation | Accepted |
| [backend 0013](../../backend/docs/adr/0013-single-identity-model-and-naming.md) | One identity model, one naming convention | Accepted |

The deferred-work register that feeds these decisions is
[`../../BACKLOG.md`](../../BACKLOG.md); an item graduates from BACKLOG to
an ADR once a direction is chosen.
