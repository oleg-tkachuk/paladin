# Diagrams

All diagrams use [Mermaid](https://mermaid.js.org/) syntax. Component names match those found in source code.

---

## 1. Context Diagram (C4-like)

```mermaid
C4Context
  title Paladin — System Context

  Person(caller, "Client (API Consumer)", "Any service calling PALADIN via HTTP REST or gRPC")

  System_Boundary(paladin, "Paladin") {
    Container(http_api, "HTTP API Server", "Gin, Port 8080", "REST API: objects, multipart, categories, health, metrics")
    Container(grpc_api, "gRPC Server", "google.golang.org/grpc, Port 9090", "Proto: Paladin service")
    Container(reaper, "Reaper Worker", "Go goroutine", "Background cleanup of expired objects, multiparts, audit logs")
    ContainerDb(postgres, "PostgreSQL", "pgxpool", "Metadata: objects, categories, idempotency, audit logs")
  }

  System_Ext(s3, "S3-Compatible Storage", "SeaweedFS (local/staging), Amazon S3 (prod)")
  System_Ext(k8s_secrets, "Kubernetes Secrets", "DB password, S3 access/secret keys")
  System_Ext(otel_collector, "OpenTelemetry Collector", "OTLP traces/metrics export (optional)")
  System_Ext(prometheus, "Prometheus", "Scrapes /metrics endpoint")

  Rel(caller, http_api, "REST API calls", "HTTP/1.1, JSON")
  Rel(caller, grpc_api, "gRPC calls", "HTTP/2, Protocol Buffers")
  Rel(http_api, postgres, "Read/write metadata", "pgx/v5")
  Rel(grpc_api, postgres, "Read/write metadata", "pgx/v5")
  Rel(http_api, s3, "Pre-sign URLs", "AWS SDK v2")
  Rel(grpc_api, s3, "Pre-sign URLs", "AWS SDK v2")
  Rel(reaper, postgres, "Delete expired records", "pgx/v5")
  Rel(reaper, s3, "Abort multipart uploads", "AWS SDK v2")
  Rel(http_api, otel_collector, "OTLP traces/metrics", "gRPC/HTTP")
  Rel(prometheus, http_api, "Scrape /metrics", "HTTP")
  Rel(paladin, k8s_secrets, "Read secrets at startup", "Kubernetes API")
```

---

## 2. Main Request Sequence — Single Object Upload

```mermaid
sequenceDiagram
    participant Client
    participant PALADIN_HTTP as PALADIN HTTP API
    participant Service as ObjectsService
    participant DB as PostgreSQL
    participant S3 as S3 Storage

    Client->>PALADIN_HTTP: POST /v1/objects {content_type, size_bytes, category}
    PALADIN_HTTP->>PALADIN_HTTP: RequestID middleware (set X-Request-Id)
    PALADIN_HTTP->>PALADIN_HTTP: AuditLog middleware (capture request)
    PALADIN_HTTP->>PALADIN_HTTP: EnforceTenant (validate X-Tenant-ID)
    PALADIN_HTTP->>PALADIN_HTTP: RateLimit (token bucket check)
    PALADIN_HTTP->>Service: CreateSingle(ctx, tenantID, category, contentType, ...)
    Service->>Service: Validate policy (content type, size, labels)
    Service->>DB: INSERT INTO objects (status=pending, expires_at=now()+TTL)
    Service->>S3: PresignPutObject(key, TTL)
    S3-->>Service: signed URL
    Service-->>PALADIN_HTTP: {id, key, upload.url, upload.method, upload.expires_at}
    PALADIN_HTTP->>DB: AuditLog middleware (write audit_logs)
    PALADIN_HTTP-->>Client: 201 Created {object_id, upload.url, ...}

    Note over Client,S3: Client uploads file directly to S3 using the pre-signed URL

    Client->>S3: PUT <presigned_url> (binary data)
    S3-->>Client: 200 OK (ETag header)

    Client->>PALADIN_HTTP: POST /v1/objects/{id}/complete {etag, size_bytes}
    PALADIN_HTTP->>Service: CompleteObject(ctx, tenantID, id, etag, sizeBytes)
    Service->>DB: UPDATE objects SET status=complete, stored_etag=..., completed_at=now()
    Service-->>PALADIN_HTTP: updated object record
    PALADIN_HTTP-->>Client: 200 OK {status: "complete", stored_etag, ...}
```

---

## 3. ER Diagram

```mermaid
erDiagram
    objects {
        UUID id PK
        TEXT tenant_id
        TEXT object_key
        TEXT bucket
        TEXT content_type
        BIGINT size_bytes
        TEXT checksum_sha256
        TEXT status
        JSONB labels
        TEXT external_ref
        TEXT category
        TEXT subpath
        TEXT stored_etag
        BIGINT stored_size_bytes
        TIMESTAMPTZ completed_at
        TIMESTAMPTZ deleted_at
        TIMESTAMPTZ expires_at
        TIMESTAMPTZ created_at
        TIMESTAMPTZ updated_at
    }

    multipart_uploads {
        UUID id PK
        TEXT tenant_id
        UUID object_id FK
        TEXT upload_id
        TEXT bucket
        TEXT object_key
        TEXT content_type
        BIGINT part_size_bytes
        TEXT status
        TIMESTAMPTZ expires_at
        TIMESTAMPTZ created_at
        TIMESTAMPTZ updated_at
    }

    multipart_parts {
        UUID multipart_id FK
        INT part_number
        TEXT etag
        BIGINT size_bytes
        TIMESTAMPTZ created_at
    }

    idempotency_keys {
        TEXT tenant_id PK
        TEXT idempotency_key PK
        TEXT request_path
        TEXT request_hash
        INT response_code
        JSONB response_body
        TIMESTAMPTZ created_at
        TIMESTAMPTZ expires_at
    }

    audit_logs {
        UUID id PK
        TEXT tenant_id
        TEXT request_id
        TEXT idempotency_key
        TEXT actor_subject
        TEXT actor_type
        INET client_ip
        TEXT user_agent
        TEXT method
        TEXT path
        JSONB query_params
        JSONB request_headers
        TEXT request_body_sha256
        BIGINT request_size_bytes
        INT http_status
        TEXT response_code
        TEXT response_status
        INT response_time_ms
        TIMESTAMPTZ created_at
    }

    object_categories {
        UUID id PK
        TEXT tenant_id
        TEXT slug
        TEXT name
        TEXT description
        TIMESTAMPTZ created_at
        TIMESTAMPTZ updated_at
    }

    objects ||--o{ multipart_uploads : "has"
    multipart_uploads ||--|{ multipart_parts : "contains"
```

---

## 4. Deployment Diagram (Kubernetes)

```mermaid
graph TD
    subgraph "Namespace: paladin"
        svc_http["Service: paladin-http\n:8080"]
        svc_grpc["Service: paladin-grpc\n:9090"]
        deploy["Deployment: paladin\n(cmd/server/main.go)"]
        secret_db["Secret: paladin-postgresql-app-user"]
        cm_config["ConfigMap: paladin config YAML\n/app/configs/paladin.yaml"]
    end

    subgraph "Namespace: database"
        pg["StatefulSet: paladin-postgresql-rw\n:5432"]
    end

    subgraph "Namespace: storage"
        seaweedfs["StatefulSet: seaweedfs-filer\n:8333"]
    end

    subgraph "Ingress / Mesh"
        ingress["Ingress / Linkerd mTLS"]
    end

    subgraph "Observability"
        otel_col["otel-collector\n:4317"]
        prometheus["Prometheus"]
    end

    ingress -->|"HTTP :8080"| svc_http
    ingress -->|"gRPC :9090"| svc_grpc
    svc_http --> deploy
    svc_grpc --> deploy
    deploy -->|"pgx/v5 TLS"| pg
    deploy -->|"S3 API"| seaweedfs
    deploy -->|"OTLP gRPC"| otel_col
    prometheus -->|"Scrape /metrics"| svc_http
    deploy -- "mounts" --> cm_config
    deploy -- "reads secret" --> secret_db
```
