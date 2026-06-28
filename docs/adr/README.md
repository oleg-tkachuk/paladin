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

The deferred-work register that feeds these decisions is
[`../../BACKLOG.md`](../../BACKLOG.md); an item graduates from BACKLOG to
an ADR once a direction is chosen.
