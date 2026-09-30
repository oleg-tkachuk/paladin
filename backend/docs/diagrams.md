# Diagrams

Drawn from the code, the Helm chart and the migrated schema. The prose that
explains them is in [ARCHITECTURE.md](../../ARCHITECTURE.md).

## Context

Paladin is a control plane: the RPCs carry metadata, and object bytes move
between the client and S3 over presigned URLs.

```mermaid
flowchart LR
    client["Application · SDK · agent · console"]
    paladin["Paladin"]
    pg[("PostgreSQL")]
    s3[("S3 backend")]
    sinks["Event sinks"]

    client -- "Connect RPC / MCP" --> paladin
    paladin -- "presigned URL" --> client
    client <== "object bytes" ==> s3
    paladin -- "bucket and object management" --> s3
    paladin --> pg
    paladin -- "events" --> sinks
    s3 -. "storage events (optional)" .-> paladin
```

## Main request: upload

```mermaid
sequenceDiagram
    participant C as Client
    participant A as serve api
    participant DB as PostgreSQL
    participant S3 as S3 backend
    C->>A: UploadObject(parent, key, content_type)
    A->>A: authenticate, Cedar, scope, quota
    A->>DB: insert object, state PENDING
    A-->>C: object + presigned PUT URL
    C->>S3: PUT bytes
    alt completion_mode EXPLICIT
        C->>A: CompleteObject
        A->>S3: HeadObject
    else completion_mode IMPLICIT
        S3-->>A: storage event (via serve ingest)
    end
    A->>DB: state ACTIVE, event row in the same transaction
```

## Schema

The core tables and their foreign keys. Rate buckets, idempotency keys, OAuth,
leases and the audit log (partitioned monthly, no foreign keys) are left out.

```mermaid
erDiagram
    tenants ||--o{ users : has
    tenants ||--o{ api_tokens : has
    tenants ||--o{ capability_records : issues
    capability_records ||--o{ capability_records : delegates
    capability_records ||--o{ charges : meters
    storage_backends ||--o{ buckets : hosts
    tenants ||--o{ buckets : owns
    buckets ||--o{ collections : stores
    tenants ||--o{ collections : has
    collections ||--o{ objects : holds
    objects ||--o{ object_versions : versions
    object_versions ||--o| object_locks : locks
    objects ||--o{ multipart_uploads : uploads
    tenants ||--o{ quotas : limits
    buckets ||--o{ quotas : limits
    tenants ||--o{ event_subscriptions : subscribes
    event_subscriptions ||--o{ event_deliveries : outbox
    tenants ||--o{ tenant_storage_migrations : migrates
```

## Deployment

What one install of both charts creates, and what it expects to find.

```mermaid
flowchart TB
    ingress["Ingress<br/>(optional)"]

    subgraph ns ["namespace"]
        console["Deployment paladin-console<br/>BFF :3000"]
        subgraph core ["paladin-core — one Deployment and Service per role"]
            api["api :8080 · :8085"]
            admin["admin :8090"]
            mcp["mcp :8095"]
            worker["worker"]
            dispatcher["dispatcher"]
            ingest["ingest (off by default)"]
        end
        jobs["Jobs, pre-install / pre-upgrade:<br/>migrate · bootstrap"]
        secrets["Secrets: signing key · bootstrap admin<br/>Postgres · S3 credentials"]
    end

    pg[("PostgreSQL — external")]
    s3[("S3 backend — external")]

    ingress -- "/paladin.iam.v1.* → :8085, rest → :8080" --> api
    ingress --> console
    console --> api & admin
    jobs --> pg
    core --> pg
    api & admin & worker --> s3
```

The admin plane has no Ingress route; the console reaches it inside the
cluster. See [docs/install.md](../../docs/install.md).
