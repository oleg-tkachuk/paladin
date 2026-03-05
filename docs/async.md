# Async & Background Jobs

**Source:** `internal/worker/reaper.go`

## Reaper Worker

The Reaper is the only background goroutine running inside the service. It is enabled/disabled via `housekeeping.enable_reaper` configuration.

### Trigger

- Runs once immediately on startup.
- Then executes a cleanup cycle every `housekeeping.gc_interval` (default: `1h`) using a `time.Ticker`.
- Stops when the root context is cancelled (SIGINT/SIGTERM).

### Cleanup Cycle (`runCleanup`)

Each cycle calls three sub-tasks in sequence:

#### 1. Pending Object Cleanup (`cleanupPending`)

| Property | Value |
|---|---|
| Trigger | Every `gc_interval` |
| Target | `objects` table where `status = 'pending'` and `expires_at < now() - pending_ttl` |
| Batch size | 100 records per cycle |
| Side effects | Deletes rows from `objects` table (hard delete via `repo.Delete`) |
| S3 cleanup | Object key may not exist yet (object never uploaded); S3 deletion is attempted but best-effort |
| TTL config | `housekeeping.pending_ttl` (default: `24h`) |

#### 2. Multipart Upload Cleanup (`cleanupMultipart`)

| Property | Value |
|---|---|
| Trigger | Every `gc_interval` |
| Target | `multipart_uploads` table where status is expired |
| Batch size | 100 records per cycle |
| Side effects | Calls `s3.AbortMultipartUpload()` to release parts in S3, then marks the DB record as `aborted` via `mpRepo.MarkAborted()` |
| TTL config | `housekeeping.multipart_ttl` (default: `72h`) |
| Idempotency | If S3 abort fails, the DB is still updated (best-effort) |

#### 3. Audit Log Pruning (`cleanupAuditLogs`)

| Property | Value |
|---|---|
| Trigger | Every `gc_interval`, only when `housekeeping.audit_log_ttl > 0` |
| Target | `audit_logs` rows where `created_at < now() - audit_log_ttl` |
| Batch size | 1000 records per cycle |
| Side effects | Hard delete from `audit_logs` via `auditRepo.Prune(cutoff, limit)` |
| TTL config | `housekeeping.audit_log_ttl` (no default — must be explicitly set) |

### Retry / Backoff

There is no retry or backoff built into the Reaper. On a failed cleanup iteration, errors are logged and the Reaper waits for the next tick.

### Database Role

The Reaper uses a separate DSN (`datastores.postgres.reaper_dsn`) when configured, which allows granting the reaper a database role with `DELETE` privileges on `audit_logs` (migration `008_audit_logs_reaper_role.sql`).

### Goroutine Lifecycle

The Reaper goroutine is started by `wire.ProvideApp()` as part of `app.Run()`. The main server context is passed in; when the context is cancelled (shutdown signal), the Reaper exits cleanly via select on `ctx.Done()`.

### Configuration Reference

| Config Key | Default | Description |
|---|---|---|
| `housekeeping.enable_reaper` | `true` | Enable/disable the reaper |
| `housekeeping.pending_ttl` | `24h` | TTL before pending objects are deleted |
| `housekeeping.multipart_ttl` | `72h` | TTL before expired multipart sessions are aborted |
| `housekeeping.audit_log_ttl` | — | TTL for audit log entries (0 = disabled) |
| `housekeeping.gc_interval` | `1h` | Cleanup cycle frequency |
| `housekeeping.delete_orphaned_parts` | `false` | Delete orphaned S3 parts from aborted multiparts |
