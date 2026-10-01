# Diagrams

Drawn from the code, the Helm chart and the migrated schema. The prose that
explains them is in [ARCHITECTURE.md](../../ARCHITECTURE.md), which also has
the per-role diagram.

Colours are consistent across diagrams: blue for clients and Kubernetes
objects, violet for the console, green for paladin-core roles, grey dashed for
components off by default, amber for stores, pink for external sinks.

## Context

Paladin is a control plane: the RPCs carry metadata, and object bytes move
between the client and S3 over presigned URLs.

```mermaid
flowchart LR
    op(["Operator · console"])
    agent(["AI agent · MCP"])
    app(["Application · SDK"])

    cp["<b>Paladin</b><br/>IAM · Cedar policy · quotas<br/>object metadata · audit · events"]

    pg[("<b>PostgreSQL</b>")]
    sinks{{"<b>Event sinks</b><br/>webhooks · brokers"}}
    s3[("<b>S3 backends</b><br/>SeaweedFS · MinIO<br/>Garage · AWS")]

    op -- "Connect RPC" --> cp
    agent -- "MCP" --> cp
    app -- "Connect RPC" --> cp
    cp --> pg
    cp -- "events" --> sinks
    cp -- "bucket and object management" --> s3
    app -. "object bytes, presigned" .-> s3

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class app,agent,op client
    class cp role
    class pg,s3 store
    class sinks external
```

## Main request: upload

The api plane authorises the call, records the object as `PENDING` and returns
a presigned URL. The object becomes `AVAILABLE` either on `CompleteObject`
(explicit) or on the storage notification `ingest` receives (implicit); in
both cases the transition and its outbox event commit together
(`statemachine.Transitioner.PromoteToAvailableInTx`).

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    box rgb(220,252,231) paladin-core
        participant A as api
        participant I as ingest
    end
    participant DB as PostgreSQL
    participant S3 as S3 backend

    C->>A: UploadObject(parent, key, content_type)
    Note over A: authenticate · Cedar · scope · quota
    A->>DB: insert object, state PENDING
    A-->>C: object + presigned PUT URL
    C->>S3: PUT bytes (presigned)
    alt completion_mode EXPLICIT
        C->>A: CompleteObject
        A->>S3: HeadObject
        A->>DB: PENDING → AVAILABLE + outbox event, one transaction
    else completion_mode IMPLICIT
        S3-)I: storage notification
        I->>DB: PENDING → AVAILABLE + outbox event, one transaction
    end
```

## Schema

The core tables and their foreign keys, from `backend/migrations`. Rate
buckets, idempotency keys, OAuth, leases, ingest dedup and the audit log
(partitioned monthly) are left out.

Identity and access:

```mermaid
erDiagram
    tenants ||--o{ users : has
    users ||--o{ refresh_tokens : holds
    tenants ||--o{ api_tokens : has
    tenants ||--o{ capability_records : issues
    capability_records ||--o{ charges : meters
    tenants ||--o| tenant_budgets : caps
```

Storage:

```mermaid
erDiagram
    storage_backends ||--o{ buckets : hosts
    tenants ||--o{ buckets : owns
    buckets ||--o{ collections : stores
    collections ||--o{ objects : holds
    objects ||--o{ object_versions : versions
    object_versions ||--o| object_locks : locks
    objects ||--o{ multipart_uploads : uploads
    buckets ||--o{ quotas : limits
    buckets ||--o{ tenant_default_bindings : target
```

Events and operations:

```mermaid
erDiagram
    tenants ||--o{ event_subscriptions : subscribes
    event_subscriptions ||--o{ event_deliveries : outbox
    tenants ||--o{ operations : runs
    tenants ||--o{ tenant_storage_migrations : migrates
    tenants ||--o{ quotas : limits
```

`capability_records` also references itself: a delegated capability points
at its parent.

## Deployment

What one install of both charts creates, and what it expects to find.

```mermaid
flowchart TB
    ingress(["Ingress or Traefik IngressRoute · optional"])

    subgraph ns ["namespace"]
        direction TB
        console["<b>paladin-console</b><br/>BFF · :3000"]
        subgraph core ["paladin-core"]
            direction LR
            api["<b>api</b><br/>:8080 · :8085"]
            admin["<b>admin</b><br/>:8090"]
            mcp["<b>mcp</b><br/>:8095"]
            worker["<b>worker</b>"]
            dispatcher["<b>dispatcher</b>"]
            ingest["<b>ingest</b> · :8100<br/>off by default"]
        end
        jobs[["Jobs: migrate · bootstrap<br/>pre-install / pre-upgrade"]]
    end

    pg[("<b>PostgreSQL</b><br/>external")]
    s3[("<b>S3 backend</b><br/>external")]

    ingress -- "/paladin.iam.v1.* → :8085<br/>rest → :8080" --> api
    ingress --> console
    console --> api
    console --> admin
    core --> pg
    jobs --> pg
    api --> s3
    admin --> s3
    worker --> s3

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    classDef k8s fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    class console ui
    class api,admin,mcp,worker,dispatcher role
    class ingest,ingress optional
    class pg,s3 store
    class jobs k8s
    style ns fill:none,stroke:#94A3B8,stroke-dasharray:3 3
    style core fill:#F8FAFC,stroke:#16A34A
```

The admin plane has no Ingress route; the console reaches it inside the
cluster. Every role except `mcp` uses PostgreSQL; `api`, `admin` and `worker`
call S3. The chart also creates Secrets for the signing key, the bootstrap
admin and the S3 credentials, and reads the PostgreSQL ones you supply. See
[docs/install.md](../../docs/install.md).
