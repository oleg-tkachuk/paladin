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
| [0006](0006-deferred-roadmap.md) | Deferred roadmap (single-binary planes, event sinks, …) | Superseded by BACKLOG.md, 0009, 0015 |
| [0007](0007-postgres-connection-headroom.md) | Postgres connection headroom & pooling ownership | Accepted |
| [0008](0008-mcp-oauth-resource-server.md) | MCP server as an OAuth 2.1 Resource Server | Accepted |
| [0009](0009-oauth-authorization-server.md) | OAuth 2.1 Authorization Server (IAM-as-AS) | Accepted |
| [0010](0010-capability-as-establishing-credential.md) | A capability may establish identity on the data plane | Accepted |
| [0011](0011-narrow-role-for-tenant-provisioning.md) | A narrow role for tenant provisioning | Accepted |
| [0012](0012-machine-principals-may-delete-their-own-objects.md) | Cedar knows the credential kind; machines may delete their own objects | Accepted |
| [0013](0013-object-lock-retention-and-legal-hold.md) | Object Lock — retention a mode of Paladin cannot lift | Accepted |
| [0014](0014-canonical-resource-names.md) | Canonical resource names (A + B + C) | Accepted |
| [0015](0015-per-tenant-bucket-layout.md) | Per-tenant bucket layout | Accepted |
| [0016](0016-cedar-tenant-membership-isolation.md) | Cedar tenant-membership isolation | Accepted |
| [0017](0017-single-identity-model-and-naming.md) | One identity model, one naming convention | Accepted |
| [0018](0018-sdk-layers.md) | The SDKs as three layers over the generated clients | Accepted |
| [0019](0019-object-search-and-read-replica.md) | Object search under RLS, and an opt-in read replica | Accepted |
| [0020](0020-sdk-integration-grade.md) | The SDKs to integration grade | Accepted |
| [0021](0021-biscuit-copies.md) | Biscuit copies — narrowed offline, revoked and counted by block | Accepted |
| [0022](0022-data-plane-acts-on-the-named-tenant.md) | The data plane acts on the tenant a platform admin names | Accepted |
| [0023](0023-one-metrics-contract.md) | One metrics contract from process to alert | Accepted |
| [0024](0024-credential-actions-and-the-capability-issuer-grant.md) | Credential actions named per resource; the capability issuer's built-in grant | Accepted |
| [0025](0025-bucket-quotas-are-platform-configuration.md) | Bucket quotas are platform configuration, outside RLS | Accepted |
| [0026](0026-storage-backend-features-are-probed.md) | A storage backend's S3 features are probed, recorded and shown | Accepted |
| [0027](0027-public-collections.md) | Public collections — anonymous reads from a public bucket | Accepted |
| [0028](0028-bucket-ownership.md) | A bucket row is a claim — Paladin registers only what it means to manage | Accepted |
| [0029](0029-capability-outside-the-request-path.md) | Capabilities for work the verifier does not see | Accepted |
| [0030](0030-agpl-with-apache-client-surface.md) | AGPL for the service, Apache-2.0 for what clients embed | Accepted |

The deferred-work register that feeds these decisions is
[`../../BACKLOG.md`](../../BACKLOG.md); an item graduates from BACKLOG to
an ADR once a direction is chosen.
