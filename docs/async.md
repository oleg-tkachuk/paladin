# Async & Jobs

The `paladin` runs lightweight background processes embedded within the same application process to manage data lifecycle and garbage collection. There are no external message brokers (like Kafka or RabbitMQ) natively required by this service; it relies on state tracking within PostgreSQL.

## 1. The Reaper

The primary async worker is the **Reaper**, which runs as a periodic background goroutine.

### Trigger

- **Schedule:** Periodic loop governed by `housekeeping.gc_interval` configuration (e.g., every 5 minutes).
- **Execution:** Started at app bootstrap (`app.Run()`).

### Responsibilities & Side Effects

1. **Pending/Soft-Deleted Object Cleanup:**
   - **Trigger State:** Objects in `pending` or `soft_deleted` status where `updated_at < (NOW() - pending_ttl)`.
   - **Side Effect:** Attempts to invoke `DeleteObject` on the S3 backend (best-effort, though the interface implies it might skip if not supported). Hard deletes the object record (`objRepo.Delete`) from the PostgreSQL database.

2. **Abandoned Multipart Uploads Cleanup:**
   - **Trigger State:** `multipart_uploads` in `initiated` state where `updated_at < (NOW() - multipart_ttl)`.
   - **Side Effect:** Invokes `AbortMultipartUpload` on the S3 backend to wipe orphan parts. Marks the DB record as `aborted`.

3. **Audit Log Pruning:**
   - **Trigger State:** Logs older than `audit_log_ttl`.
   - **Side Effect:** Hard deletes old records from the `audit_logs` table.

### Operations Characteristics

- **Retry Policy & Backoff:** The Reaper processes records in batches (e.g., limit 100). If a cleanup action fails (e.g., S3 network timeout), it logs an error but proceeds. The failed record remains in the database and will be retried automatically on the next periodic `gc_interval` tick.
- **DLQ Behavior:** There is no explicit Dead Letter Queue. Un-deletable records continue to surface in the Reaper's queries until resolved.
- **Idempotency:** Actionable states (like `AbortMultipartUpload`) are inherently idempotent at the S3 API level. DB actions rely on standard transactional correctness.
