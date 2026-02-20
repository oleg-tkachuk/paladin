# Database Documentation

The `paladin` service uses PostgreSQL to manage object metadata, multipart upload states, idempotency, and audit logs.

## Entity Relationship Diagram

```mermaid
erDiagram
    objects ||--o { multipart_uploads : owns
    multipart_uploads ||--o { multipart_parts : contains
    objects ||--o { audit_logs : "associated with"
    objects ||--o { idempotency_keys : "verified by"
```

## Tables

### 1. `objects`

Stores the primary metadata and lifecycle status for all objects.

| Column              | Type        | Description                                                                  |
| :---                | :---        | :---                                                                         |
| `id`                | UUID        | Primary Key.                                                                 |
| `tenant_id`         | TEXT        | For isolation.                                                               |
| `object_key`        | TEXT        | Unique path in S3.                                                           |
| `bucket`            | TEXT        | S3 bucket name.                                                              |
| `content_type`      | TEXT        | MIME type (e.g. image/jpeg).                                                 |
| `size_bytes`        | BIGINT      | Expected size.                                                               |
| `stored_size_bytes` | BIGINT      | Actual size in storage (v1.1.0).                                             |
| `checksum_sha256`   | TEXT        | Optional integrity checksum.                                                 |
| `stored_etag`       | TEXT        | ETag from S3 after completion (v1.1.0).                                      |
| `status`            | TEXT        | `pending`, `uploading`, `uploaded`, `complete`, `aborted`, `soft_deleted`, `hard_deleted`, `error`. |
| `labels`            | JSONB       | Key-value metadata.                                                          |
| `external_ref`      | TEXT        | Client-provided opaque ID.                                                   |
| `created_at`        | TIMESTAMPTZ | Creation time.                                                               |
| `updated_at`        | TIMESTAMPTZ | Last update timestamp.                                                       |
| `completed_at`      | TIMESTAMPTZ | Upload completion time.                                                      |
| `deleted_at`        | TIMESTAMPTZ | Soft-deletion time.                                                          |
| `expires_at`        | TIMESTAMPTZ | Upload session TTL.                                                          |

**Indexes:**

- `idx_objects_tenant_created`: `(tenant_id, created_at DESC)` (v1.2.0)
- `idx_objects_tenant_status_created`: `(tenant_id, status, created_at DESC)` (v1.1.0)
- `idx_objects_tenant_external_ref`: `(tenant_id, external_ref) WHERE external_ref IS NOT NULL` (v1.1.0)
- `idx_objects_pending_expires`: `(expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL` (v1.1.0)
- `uq_objects_tenant_key`: `(tenant_id, object_key)` UNIQUE
- `uq_objects_tenant_external_ref`: `(tenant_id, external_ref)` UNIQUE

---

### 2. `multipart_uploads`

Tracks active S3 multipart upload sessions.

| Column        | Type        | Description         |
| :---          | :---        | :---                |
| `id`              | UUID        | Primary key.                                          |
| `tenant_id`       | TEXT        | Owner.                                                |
| `object_id`       | UUID        | References `objects(id)` (CASCADE).                   |
| `upload_id`       | TEXT        | S3 Upload ID.                                         |
| `status`          | TEXT        | `initiated`, `uploaded`, `completed`, `aborted`, `expired`. |
| `part_size_bytes` | BIGINT      | Size of each part.                                    |
| `expires_at`      | TIMESTAMPTZ | Session expiration.                                   |

**Indexes:**

- `idx_multipart_tenant_status`: `(tenant_id, status, created_at DESC)` (v1.1.0)
- `idx_multipart_upload_id`: `(upload_id)` (v1.1.0)
- `idx_multipart_initiated_expires`: `(expires_at) WHERE status = 'initiated'` (v1.2.0)

---

### 3. `multipart_parts`

Stores information about individual parts of a multipart upload (optional tracking).

| Column         | Type    | Description                       |
| :---           | :---    | :---                              |
| `multipart_id` | UUID    | References `multipart_uploads(id)` (CASCADE). |
| `part_number`  | INTEGER | Part sequence (1-indexed).        |
| `etag`         | TEXT    | S3 ETag for this part.            |
| `size_bytes`   | BIGINT  | Part size.                        |

---

### 4. `audit_logs`

Comprehensive log of all API requests and responses for a tenant.

| Column           | Type        | Description           |
| :---             | :---        | :---                  |
| `id`               | UUID        | Primary key.                |
| `tenant_id`        | TEXT        | Tenant ID.                  |
| `request_id`       | TEXT        | Correlation ID.             |
| `actor_subject`    | TEXT        | Who performed the action.   |
| `method`           | TEXT        | HTTP Method.                |
| `path`             | TEXT        | API Path.                   |
| `http_status`      | INTEGER     | Response status code.       |
| `response_time_ms` | INTEGER     | Latency.                    |
| `created_at`       | TIMESTAMPTZ | Event time.                 |

**Indexes:**

- `idx_audit_logs_tenant_created_at`: `(tenant_id, created_at DESC)`
- `idx_audit_logs_tenant_path_created_at`: `(tenant_id, path, created_at DESC)`
- `idx_audit_logs_tenant_method_created`: `(tenant_id, method, created_at DESC)`
- `idx_audit_logs_created_at`: `(created_at)` (Reaper index)

---

### 5. `idempotency_keys`

Ensures at-most-once semantics for mutation operations.

| Column | Type | Description |
| :--- | :--- | :--- |
| `tenant_id` | TEXT | Tenant ID. |
| `idempotency_key` | TEXT | Unique key provided by client. |
| `response_code` | INTEGER | Buffered response status. |
| `response_body` | JSONB | Buffered response payload. |
| `expires_at` | TIMESTAMPTZ | Cleanup time. |

---

## Security

- **Row Level Security (RLS)**: Enabled on all tables to ensure strict tenant isolation at the database level.
- **Policies**: `tenant_id = current_setting('app.tenant_id')` must match for all operations.
