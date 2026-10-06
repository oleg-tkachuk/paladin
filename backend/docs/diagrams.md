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

## Code layout: handlers own their ports

A handler package declares the interfaces it needs; the Postgres and S3
adapters implement them. That is why `internal/store` and `internal/storage`
import `internal/api`: the arrow points at the interface, not the caller.

```mermaid
flowchart LR
    cmd["<b>cmd/server</b><br/>serve &lt;role&gt;"]
    app["<b>internal/app</b><br/>fx wiring · listeners"]
    shim["<b>connectshim</b><br/>proto ⇄ domain"]
    h["<b>handlers</b><br/>internal/api/&lt;plane&gt;/v1/*h<br/>declare ports"]
    pgad["<b>store/postgres/adapters</b><br/>over sqlc"]
    s3ad["<b>storage/s3adapter</b><br/>routed per backend"]
    pg[("<b>PostgreSQL</b>")]
    s3[("<b>S3</b>")]

    cmd --> app --> shim --> h
    app -- "injects" --> pgad & s3ad
    pgad -. "implements" .-> h
    s3ad -. "implements" .-> h
    pgad --> pg
    s3ad --> s3

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class cmd,app,shim,h role
    class pgad,s3ad optional
    class pg,s3 store
```

## Request path

One data-plane call, outermost first (`internal/app/build_listeners_api.go`).
Every response carries the release in `X-Paladin-Version`, and a procedure the
release does not serve is answered with a Connect `Unimplemented` rather than a
bare 404, so a client can name both sides of a contract skew
(`middleware.ServerVersion`, `middleware.UnknownProcedure`). An error the
handler returns from a domain sentinel goes out with a `google.rpc.ErrorInfo`
whose reason is a `paladin.common.v1.ErrorReason` (`apiutil.MapError`). The iam plane lets `Login`,
`RefreshToken` and `ExchangeAudience` through anonymously and adds a login rate
limit and an audit of mutations; the admin plane accepts API tokens only when
they carry roles, and audits its mutations too. On the data plane a platform
admin's request acts on the tenant it names (`middleware.ActOnNamedTenant`),
which every later step keys on, and such calls inside another tenant are
audited there (`middleware.AuditActingElsewhere`).

```mermaid
flowchart TB
    req(["Connect RPC"])
    ver["<b>HTTP</b><br/>X-Paladin-Version · unknown procedure → Unimplemented"]
    subgraph ic ["interceptors"]
        direction TB
        obs["otel · outcome log"]
        authn["<b>authenticate</b><br/>JWT · API token · capability → Principal"]
        aud["audience must be this plane"]
        act["platform admin: act on the named tenant"]
        lim["tenant rate limit · quota soft check"]
        val["protovalidate · idempotency key"]
        aae["audit a platform admin acting elsewhere"]
    end
    h["<b>handler</b>"]
    cedar["<b>Cedar</b><br/>tenant · bucket · collection policies<br/>+ built-in scope forbid"]
    cap["<b>capability</b><br/>allowed op · charge budget"]
    adp["adapter"]
    pool["RLS pool<br/>paladin.tenant_id set on acquire"]
    pg[("<b>PostgreSQL</b><br/>row-level security")]

    err["<b>MapError</b><br/>Connect code + ErrorInfo reason"]

    req --> ver --> obs --> authn --> aud --> act --> lim --> val --> aae --> h
    h --> cedar
    h --> cap
    h --> adp --> pool --> pg
    h -. "a domain error" .-> err

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class req client
    class ver,obs,authn,aud,act,lim,val,aae,h,cedar,cap,adp,pool,err role
    class pg store
    style ic fill:#F8FAFC,stroke:#16A34A
```

Policies are compiled once per tenant and cached; a `LISTEN policy_changed`
connection evicts them, and flushes everything after a reconnect. A mutation
writes its outbox row in the same transaction as the change.

## Main request: upload

The api plane authorises the call, records the object as `PENDING` and returns
a presigned URL. The object becomes `AVAILABLE` either on `CompleteObject`
(explicit) or on the storage notification `ingest` receives (implicit); in
both cases the transition and its outbox event commit together
(`statemachine.Transitioner.PromoteToAvailableInTx`). The SDKs send the
content's SHA-256 with `CompleteObject`, and verify a whole download against
it.

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
        C->>A: CompleteObject(etag, SHA-256 of the bytes)
        A->>S3: HeadObject
        A->>DB: PENDING → AVAILABLE + checksum + outbox event, one transaction
    else completion_mode IMPLICIT
        S3-)I: storage notification
        I->>DB: PENDING → AVAILABLE + outbox event, one transaction
    end
```

## Events

Every producer writes the event row in the transaction that made the change;
`dispatcher` delivers it at least once. `ingest` runs the other way, turning
storage notifications into state changes. The audit log has its own table and
a live stream.

```mermaid
flowchart LR
    notif(["storage notification<br/>webhook · NATS · RabbitMQ · SQS"])
    subgraph prod ["producers"]
        direction TB
        api["<b>api</b>"]
        admin["<b>admin</b>"]
        worker["<b>worker</b>"]
        ingest["<b>ingest</b><br/>dedup · PENDING → AVAILABLE"]
    end
    outbox[("<b>event_deliveries</b><br/>pending · delivered · failed")]
    disp["<b>dispatcher</b><br/>SKIP LOCKED batches · backoff"]
    sinks{{"<b>sinks</b><br/>HTTP · NATS · Kafka<br/>RabbitMQ · SQS"}}
    audit[("<b>audit_log</b>")]
    sse["admin: audit stream<br/>LISTEN paladin_audit → SSE"]

    notif --> ingest
    api & admin & worker & ingest --> outbox --> disp --> sinks
    api & admin --> audit --> sse

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class notif client
    class api,admin,worker,disp,sse role
    class ingest optional
    class outbox,audit store
    class sinks external
    style prod fill:#F8FAFC,stroke:#16A34A
```

A row that exhausts its attempts stays `failed`; nothing redrives it. Consumers
deduplicate on the event id ([event-delivery-dedup.md](../../docs/event-delivery-dedup.md)).

## Background work

`worker` runs each job in one replica at a time, under a lease in
`worker_leases`. Jobs use the BYPASSRLS reaper pool, because they act across
tenants. The job list is in [ops-housekeeping.md](ops-housekeeping.md).

```mermaid
flowchart LR
    w["<b>worker</b> replicas"]
    lease[("<b>worker_leases</b><br/>one per job · 30s TTL")]
    jobs["<b>jobs</b><br/>reconcilers · purgers · drainers<br/>lifecycle · quotas · operations"]
    pool["reaper pool<br/>BYPASSRLS"]
    pg[("<b>PostgreSQL</b>")]
    s3[("<b>S3</b>")]

    w -- "claim · renew every 10s" --> lease
    w --> jobs --> pool --> pg
    jobs --> s3

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class w,jobs,pool role
    class lease,pg,s3 store
```

## Console session

The browser holds only short-lived access tokens; the refresh token stays in an
httpOnly cookie the BFF reads. Every plane call goes through the BFF.

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    box rgb(237,233,254) paladin-console
        participant P as proxy.ts
        participant F as BFF /api
    end
    participant I as iam
    participant D as data · admin

    B->>P: any page or /api call
    Note over P: no paladin_rt_iam cookie → /login<br/>unsafe /api call from a foreign Origin → 403
    B->>F: /api/auth/login
    F->>I: Login
    F-->>B: iam access token · Set-Cookie paladin_rt_iam
    B->>F: /api/auth/exchange {audience}
    F->>I: ExchangeAudience
    F-->>B: access token for that plane
    B->>F: /api/rpc/{plane}/… with Bearer
    Note over F: token audience must match the plane
    F->>D: Connect · Authorization · Idempotency-Key · X-Forwarded-For
    D-->>B: response
```

## Contract fan-out

`proto/` is the one contract. Its generators each have a drift gate in
`verify-all`; `verify:proto-breaking` refuses a wire-incompatible change
against the last contract tag. Each SDK also generates a facade — a client for
every service of each plane — from the stubs, so a new service needs no
hand-written wiring. The Python stubs are generated with the protoc of
`grpcio-tools` 1.81.1, whose gencode version is the protobuf floor the package
declares (6.33.5), so they run on protobuf 6 and 7.

```mermaid
flowchart LR
    proto["<b>proto/</b><br/>incl. ErrorReason"]
    es["<b>frontend/src/gen</b><br/>protoc-gen-es"]
    gosdk["<b>sdk/go/gen</b><br/>protoc-gen-go · connect-go"]
    gofacade["<b>facade_gen.go</b><br/>internal/facadegen"]
    py["<b>sdk/python</b> stubs<br/>grpcio-tools protoc · connect-python"]
    pyfacade["<b>facade.py</b><br/>scripts/gen_facade.py"]
    console["<b>console</b>"]
    backend["<b>backend</b><br/>go.mod replace → sdk/go<br/>ErrorInfo reasons"]

    proto --> es --> console
    proto --> gosdk --> backend
    gosdk --> gofacade
    proto --> py --> pyfacade

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef ui fill:#EDE9FE,stroke:#7C3AED,color:#3B0764
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class proto,gosdk,gofacade,py,pyfacade,backend role
    class es,console ui
```

## SDK layers

Both SDKs are the same stack of layers over the generated clients
([ADR-0018](../../docs/adr/0018-sdk-layers.md)). The RPCs and the presigned
transfers are separate legs with separate transports: a presigned request goes
to storage, not to Paladin, and carries none of the client's credentials.

```mermaid
flowchart TB
    app(["Application"])

    subgraph rpc ["RPC leg — to Paladin"]
        direction TB
        facade["<b>Connect / connect</b><br/>a client per service per plane"]
        mw["<b>interceptors</b><br/>typed errors · token per call · idempotency key<br/>retries · User-Agent · hooks"]
        stubs["generated stubs"]
        tlsr["<b>HTTP transport</b><br/>TLS: CA bundle · client cert re-read on rotation<br/>SPIFFE ID (Go)"]
    end

    wf["<b>workflows</b><br/>Upload · Download · DownloadMany · Pages · Wait"]

    subgraph xfer ["presigned leg — to storage"]
        direction TB
        tr["<b>Transfer</b><br/>bounded timeouts · no redirects<br/>split horizon · TLS · hooks"]
        chk["verify size + checksum<br/>ranges"]
    end

    names["<b>names</b><br/>resource names · paladin:// URIs"]
    fake["<b>paladintest · paladin.testing</b><br/>in-memory data plane"]

    paladin[["<b>Paladin</b> planes"]]
    s3[("<b>S3 backend</b>")]

    app --> facade --> mw --> stubs --> tlsr --> paladin
    app --> wf
    wf --> facade
    wf --> tr --> s3
    tr --- chk
    app -.-> names
    fake -. "stands in for" .-> paladin

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    class app client
    class facade,mw,tlsr,stubs,wf,tr,chk,names role
    class fake optional
    class paladin role
    class s3 store
    style rpc fill:#F8FAFC,stroke:#16A34A
    style xfer fill:#F8FAFC,stroke:#16A34A
```

Both SDKs run the same scenarios against the stack in CI
(`sdk/testdata/scenarios.json`), and parse names against the same table
(`sdk/testdata/names.json`); where they differ on purpose, the READMEs say so.

## Schema

The core tables and their foreign keys, from `backend/migrations`. Left out:
rate buckets, idempotency keys, OAuth, DPoP replay (`dpop_seen_jti`), leases,
ingest dedup, the audit log (partitioned monthly), the tag catalogue
(`object_tags`), slug history, backend health, replication state, and the
purge and multipart-abort queues (`pending_purges`, `pending_multipart_aborts`).

Identity and access:

```mermaid
erDiagram
    tenants ||--o{ users : has
    users ||--o{ refresh_tokens : holds
    users ||--o| user_settings : has
    tenants ||--o{ api_tokens : has
    tenants ||--o{ capability_records : issues
    capability_records ||--o| capability_revocations : revokes
    capability_records ||--o{ capability_biscuit_revocations : "revokes a copy"
    capability_records ||--o| capability_usage : counts
    capability_records ||--o{ capability_copy_usage : "counts a copy"
    capability_records ||--o{ capability_reservations : holds
    capability_records ||--o{ charges : meters
    charges ||--o{ charge_refunds : refunds
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
    buckets ||--o| bucket_quotas : limits
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
at its parent. The two `copy` tables are keyed by a Biscuit block's revocation
id rather than a uuid: a copy exists only in its holder's hands, and the id is
all the server ever sees of it (ADR-0021).

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
    ingress -- "/paladin.admin.v1.* → :8090<br/>IngressRoute only" --> admin
    ingress --> console
    console --> api
    console --> admin
    mcp -- "bridge mode" --> api & admin
    admin -. "ops :8099 · /stats census" .-> worker
    admin -. "ops :8099 · queue stats" .-> dispatcher
    admin -. "MCP sessions" .-> mcp
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

The standard Ingress routes only the api role. The Traefik IngressRoute also
routes `/paladin.admin.v1.*` to `admin` on the shared host, or moves the admin
plane to its own host when `traefik.planes.admin.hosts` is set; either way the
console reaches `admin` inside the cluster. Every role except `mcp` (in bridge
mode, as the chart runs it) uses PostgreSQL; `api`, `admin` and `worker` call
S3.

`admin` proxies three views the console reads from other roles' listeners —
the stats behind the platform-admin role, the sessions behind a Cedar check:
the cross-tenant `/stats` census and
the tenants behind each count it flags, from the worker
(`SystemService.GetPlatformStats` and `ListPlatformStatsTenants` →
`worker.ops_url` → `/system/rls-census.json` and
`/system/rls-census/tenants.json`, served by `internal/platformstats` on the
worker's BYPASSRLS pool); the outbox queue view, from the dispatcher
(`GetDispatcherStats` → `dispatcher.ops_url`); and live MCP sessions, from
`mcp` (`MCPInspectService.ListSessions` → `mcp.http.sessions_url`). The
worker's Service publishes `:8099` and targets container port `:8090`.

The chart also creates Secrets for the signing key, the bootstrap
admin and the S3 credentials, and reads the PostgreSQL ones you supply. See
[docs/install.md](../../docs/install.md).
