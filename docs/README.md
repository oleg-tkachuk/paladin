# Documentation

Start with [ARCHITECTURE.md](../ARCHITECTURE.md) for what the system is
and where its boundaries are. This directory holds the detail.

## Reference

- [install.md](install.md) — installing on Kubernetes with the Helm
  charts: prerequisites, database roles, the values an install needs.
- [upgrading.md](upgrading.md) — breaking changes between releases and
  what to do about them. Read before upgrading an existing deployment.
- [releasing.md](releasing.md) — what each tag family publishes, who
  cuts it, and why the product, the SDKs and `capability/` are versioned
  apart.
- [configuration.md](configuration.md) — every configuration surface:
  files, overlays, environment overrides, secrets, and the validation
  that runs at load.
- [`../backend/README.md`](../backend/README.md) — roles, ports, package
  layout, wire contracts, database.
- [`../frontend/README.md`](../frontend/README.md) — console and BFF.
- [`../capability/README.md`](../capability/README.md) — the standalone
  authorisation primitive, usable without the rest of Paladin.

## Subsystems

- [storage-ingest.md](storage-ingest.md) — storage-event sources, wire
  formats and configuration.
- [event-delivery-dedup.md](event-delivery-dedup.md) — at-least-once
  delivery and the dedup contract a consumer must implement.
- [event-bus-jetstream.md](event-bus-jetstream.md) — the JetStream event
  bus.
- [deletion-semantics.md](deletion-semantics.md) — what each delete
  destroys, what refuses to be deleted and why, and where the bytes go.

## Architecture decisions

Numbered, immutable once accepted, superseded rather than edited. See
[adr/README.md](adr/README.md) for the format.

| ADR | Subject |
| --- | --- |
| [0001](adr/0001-otel-observability-baseline.md) | OpenTelemetry observability baseline |
| [0002](adr/0002-api-error-connect-mapping.md) | Centralised error → Connect-code mapping |
| [0003](adr/0003-transactional-outbox.md) | Transactional event outbox |
| [0004](adr/0004-table-backed-audit-outbox.md) | Crash-durable (table-backed) audit outbox |
| [0005](adr/0005-cnpg-ha-ownership.md) | CNPG Postgres HA ownership & verify-full TLS |
| [0006](adr/0006-deferred-roadmap.md) | Deferred roadmap |
| [0007](adr/0007-postgres-connection-headroom.md) | Postgres connection headroom & pooling ownership |
| [0008](adr/0008-mcp-oauth-resource-server.md) | MCP server as an OAuth 2.1 Resource Server |
| [0009](adr/0009-oauth-authorization-server.md) | OAuth 2.1 Authorization Server (IAM-as-AS) |
| [0010](adr/0010-capability-as-establishing-credential.md) | A capability may establish identity on the data plane |
| [0011](adr/0011-narrow-role-for-tenant-provisioning.md) | A narrow role for tenant provisioning |
| [0012](adr/0012-machine-principals-may-delete-their-own-objects.md) | Cedar knows the credential kind, and machines may delete their own objects |
| [0013](adr/0013-object-lock-retention-and-legal-hold.md) | Object Lock — retention a mode of Paladin cannot lift |
| [0014](adr/0014-canonical-resource-names.md) | Canonical resource names |
| [0015](adr/0015-per-tenant-bucket-layout.md) | Per-tenant bucket layout |
| [0016](adr/0016-cedar-tenant-membership-isolation.md) | Cedar tenant-membership isolation |
| [0017](adr/0017-single-identity-model-and-naming.md) | One identity model, one naming convention |

## Runbooks

For when something is already on fire.

- [worker-stalled.md](runbooks/worker-stalled.md) — background worker
  stalled or failing.
- [quota-usage-drift.md](runbooks/quota-usage-drift.md) — a tenant hits
  its quota while storing almost nothing.
- [platform-stats-unavailable.md](runbooks/platform-stats-unavailable.md)
  — the console's `/stats` page reports "census unavailable".
- [partition-audit-idempotency.md](runbooks/partition-audit-idempotency.md)
  — partitioning `audit_log` and `idempotency_keys`.
- [sqs-sink-credentials.md](runbooks/sqs-sink-credentials.md) — SQS
  event-sink credentials, on and off AWS.
- [no-container-metrics-on-orbstack.md](runbooks/no-container-metrics-on-orbstack.md)
  — why every `container_*` panel is empty on the dev cluster, and what to
  use instead.

## Elsewhere in the repository

- [BACKLOG.md](../BACKLOG.md) — deferred work, with Status / Reason /
  Definition of Done / Blockers for each entry. Consult it before
  concluding that a gap is an oversight.
- [specs/](../specs/) — spec-driven-development artifacts per feature:
  specification, research, data model, plan, tasks. Useful when you want
  to know what alternatives a feature considered.
- [security-model.md](security-model.md) — the security model; how to report is in [SECURITY.md](../.github/SECURITY.md)
- [development.md](development.md) — conventions, test tiers and the gates
