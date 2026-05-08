# Async & Jobs - Paladin (PALADIN)

## Overview

PALADIN uses internal background workers to manage object lifecycle tasks that don't need to happen synchronously with request processing.

## Current Workers

### 1. Reaper (Object GC)

- **Implementation**: [../internal/worker/reaper.go](../internal/worker/reaper.go)
- **Purpose**: Cleans up metadata and physical objects that are no longer needed.
- **Triggers**:
  - `pending_ttl`: Purgers objects stuck in `pending` state for too long.
  - `multipart_ttl`: Aborts expired multipart upload sessions in S3/SeaweedFS.
- **Frequency**: Configurable via `housekeeping.gc_interval`.

### 2. Audit Log Archiver (TBD)

- **Planned**: Move older audit logs from Postgres to cold storage.

## Async Patterns

PALADIN relies on **S3 Post-Object Deletion** and **Pre-signed URLs** to offload heavy I/O tasks.

- **Upload Completion**: When a client completes an upload, it notifies PALADIN asynchronously. PALADIN then verifies the size/etag from S3 and updates the metadata record to `active`.
- **Soft Delete**: Deletion is marked immediately in Postgres. The physical deletion from S3 can happen asynchronously via the Reaper or a scheduled purge request.

## Resilience

- **Idempotency**: All mutation operations (Complete, Purge, Update) support an optional `Idempotency-Key` to prevent duplicate processing of async tasks.
- **Retry Mechanism**: The Reaper uses a simple exponential backoff for failed cleanup tasks.
