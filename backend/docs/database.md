# Database - Paladin

## Overview

Paladin uses **PostgreSQL** as its primary metadata store. The schema is optimized for multi-tenant isolation and object lifecycle tracking.

## Tables

### 1. `objects`

The primary table for object metadata.

- **PK**: `id` (UUID)
- **Indices**:
  - `idx_objects_tenant_created_at`: For performant listing and filtering by tenant.
  - `uq_objects_tenant_key`: Enforces unique object keys within a single tenant.
- **Statuses**: `pending`, `uploading`, `uploaded`, `complete`, `aborted`, `deleted`, `error`, `soft_deleted`, `hard_deleted`.

### 2. `multipart_uploads`

Tracks the state of active multi-part upload sessions.

- **Retention**: Expired uploads are purged by the Reaper worker.

### 3. `multipart_parts`

Child table for `multipart_uploads` to track individual uploaded parts and their ETags.

### 4. `tenants`

Stores tenant-specific configuration and metadata (e.g., specific S3 object_keys or quotas).

### 5. `audit_logs`

Structured audit trail for all object mutations.

---

## Data Lifecycle

### Soft vs Hard Delete

- **Soft Delete**: Marks the record as `deleted` in the `objects` table. The object remains in S3.
- **Hard Delete (Purge)**: Immediately removes the metadata record and triggers the physical removal of the object from S3.

### Housekeeping (Reaper)

The Reaper worker runs periodically to:

1. Purge `pending` objects that have exceeded their time-to-live (`pending_ttl`).
2. Abort and cleanup expired multipart uploads.
3. (Optional) Permanently delete files from storage that were soft-deleted beyond the retention period.

## Migrations

Managed using [Goose](https://github.com/pressly/goose). Migrations are located in [../migrations/](../migrations/).

- **Latest Version**: See current directory listing for migration count (021_relax_object_tag_constraints.sql as of last check).
