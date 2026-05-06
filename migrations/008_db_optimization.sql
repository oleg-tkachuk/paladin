-- +goose NO TRANSACTION
-- +goose Up

-- ─── 008: performance + security hardening ─────────────────────────────────
--
-- Indexes are created CONCURRENTLY so a live database can ride this
-- migration without blocking writes. Goose's default transaction wrapper
-- is disabled (NO TRANSACTION) because CONCURRENTLY can't run inside one.
--
-- Audit checklist driven this slice:
--   1. Cover predicate columns hit by housekeeping reapers (audit_log.at,
--      api_keys.expires_at, refresh_tokens.expires_at, operations.done_at)
--      with partial indexes that skip rows the reaper never touches.
--   2. Cover tenant-scoped reads (audit_log by actor_tenant_id) — without
--      this every Cedar-permitted cross-tenant ReadAuditLog scans the
--      table.
--   3. fillfactor lowered on tables with frequent in-place UPDATEs so
--      Postgres's HOT (heap-only-tuple) optimisation stays effective:
--      avoids index churn on every state transition.
--   4. Trigger functions get `SET search_path = pg_catalog, public` to
--      defend against schema-injection (a malicious schema earlier in
--      the search_path could shadow built-ins). PG security best practice.
--   5. CHECK constraints on enum-like TEXT columns so bad data is rejected
--      at write time, not at the application layer alone.
--   6. STATISTICS targets bumped on hot JSONB columns so the planner
--      picks better paths over `tags @>` / `metadata @>` predicates.

-- ─── A. Predicate-coverage indexes ─────────────────────────────────────────

-- Tenant-scoped audit reads (Cedar ReadAuditLog with actor_tenant_id match).
-- Without this every tenant.admin scrolling their own audit log scans the
-- whole table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_tenant_at
    ON audit_log (actor_tenant_id, at DESC)
    WHERE actor_tenant_id IS NOT NULL;

-- AuditLogPurger reaper: `WHERE at < cutoff`. The existing idx_audit_log_at
-- is `at DESC` which scans backwards just fine for the reaper's range
-- delete; no extra index needed. Kept this comment so future maintainers
-- don't add a duplicate.

-- ApiKeyExpirer reaper: `WHERE expires_at < now() AND revoked = FALSE`.
-- Partial index keeps it tight (revoked rows skip the index entirely).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_api_keys_active_expiry
    ON api_keys (expires_at)
    WHERE revoked = FALSE AND expires_at IS NOT NULL;

-- RefreshTokenPurger reaper: `WHERE expires_at < now()`. Replaces the
-- non-partial idx_refresh_tokens_expiry with a tighter version that skips
-- already-revoked tokens (still fetched by user_id when needed).
DROP INDEX CONCURRENTLY IF EXISTS idx_refresh_tokens_expiry;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_refresh_tokens_active_expiry
    ON refresh_tokens (expires_at)
    WHERE revoked = FALSE;

-- Operations housekeeping: completed ops older than retention. Partial
-- index excludes still-running operations (the bulk of recent rows).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_operations_terminal_done_at
    ON operations (done_at)
    WHERE done_at IS NOT NULL;

-- Object-versions delete-marker lookups — RestoreObject walks history
-- looking for the most recent delete marker per object. The existing
-- idx_object_versions_object_id_created can serve this scan, but a
-- partial index on the marker condition is ~10× tighter.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_object_versions_delete_markers
    ON object_versions (object_id, created_at DESC)
    WHERE is_delete_marker = TRUE;

-- Reconciler scan: `WHERE state = 'PENDING' AND created_at < now() - $age`.
-- The existing idx_objects_state_pending_expiry is keyed on
-- presign_expires_at — fine for the presign-aging path but doesn't help
-- the reconciler that orders by created_at. Add a tenant-aware partial
-- index keyed on the columns the reconciler actually reads.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_pending_created
    ON objects (created_at)
    WHERE state = 'PENDING';

-- FAILED state probes — reconciler decides whether to retry. Tiny table
-- partition; cheap to maintain.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_objects_failed_updated
    ON objects (updated_at)
    WHERE state = 'FAILED';

-- ─── B. Drop redundancies surfaced during the audit ────────────────────────

-- idx_refresh_tokens_expiry replaced above by the partial-index variant.

-- ─── C. fillfactor for HOT-update friendliness ─────────────────────────────
--
-- Default fillfactor is 100 — every UPDATE creates a new tuple and any
-- row-pointer indexes need updating. fillfactor < 100 leaves headroom on
-- the page so the new tuple lands on the same page (HOT update), which
-- skips index maintenance. Tradeoff: slightly more storage.
--
-- Targeted at the five hottest write paths: object state machine,
-- per-tenant quota counters, IAM logins (last_login_at), api-key
-- usage tracking, and operation state transitions.

ALTER TABLE objects             SET (fillfactor = 80);
ALTER TABLE quotas              SET (fillfactor = 70);
ALTER TABLE users               SET (fillfactor = 85);
ALTER TABLE api_keys            SET (fillfactor = 85);
ALTER TABLE operations          SET (fillfactor = 80);
ALTER TABLE multipart_uploads   SET (fillfactor = 85);
ALTER TABLE event_subscriptions SET (fillfactor = 90);

-- ─── D. STATISTICS targets on hot JSONB columns ────────────────────────────
-- Default stats target (100) under-samples high-cardinality JSONB. The
-- planner picks bad paths for `tags @> '{"...":"..."}'` predicates that
-- are common in CEL filters.
ALTER TABLE objects ALTER COLUMN tags     SET STATISTICS 1000;
ALTER TABLE objects ALTER COLUMN metadata SET STATISTICS 1000;
ALTER TABLE tenants ALTER COLUMN labels   SET STATISTICS 500;

-- ─── E. Trigger function hardening (search_path injection defence) ─────────
-- Replace the existing functions with versions that pin search_path. No
-- behaviour change — same body. The pin ensures `now()`, `RAISE`, etc.
-- always resolve to pg_catalog regardless of session search_path.

-- StatementBegin/End wraps each CREATE FUNCTION because goose's
-- auto-splitter splits on `;` and would otherwise treat the inner
-- semicolons in the PL/pgSQL bodies as statement boundaries — symptom
-- is `unterminated dollar-quoted string at or near "$$"` on a fresh
-- DB run. Note: this migration was previously applied to clusters
-- bootstrapped with the old `acme` DB, so goose never re-parsed
-- it; the bug only surfaced once the new `paladin` DB triggered a clean
-- replay from migration 001.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_resource_version() RETURNS trigger AS $$
BEGIN
    IF NEW.resource_version IS NULL OR NEW.resource_version = OLD.resource_version THEN
        NEW.resource_version := OLD.resource_version + 1;
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, public;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_object_key_bucket_tenancy() RETURNS TRIGGER AS $$
DECLARE
    bucket_owner UUID;
BEGIN
    SELECT owner_tenant_id INTO bucket_owner
    FROM buckets
    WHERE backend_id = NEW.backend_id AND bucket_name = NEW.bucket_name;

    IF bucket_owner IS NOT NULL AND bucket_owner <> NEW.tenant_id THEN
        RAISE EXCEPTION 'object_key tenant_id % cannot bind to bucket owned by tenant %',
            NEW.tenant_id, bucket_owner USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, public;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_object_version_lock() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.legal_hold IS DISTINCT FROM NEW.legal_hold) THEN
        IF OLD.legal_hold THEN
            RAISE EXCEPTION 'object version % is under legal hold', OLD.version_id
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.lock_mode = 'COMPLIANCE' AND OLD.lock_retain_until IS NOT NULL AND OLD.lock_retain_until > now() THEN
            RAISE EXCEPTION 'compliance lock active on version % until %', OLD.version_id, OLD.lock_retain_until
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.lock_mode = 'GOVERNANCE' AND OLD.lock_retain_until IS NOT NULL AND OLD.lock_retain_until > now() THEN
            IF NOT COALESCE(current_setting('paladin.governance_bypass', true)::boolean, false) THEN
                RAISE EXCEPTION 'governance lock active on version % until %', OLD.version_id, OLD.lock_retain_until
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, public;
-- +goose StatementEnd

-- ─── F. CHECK constraints — enum-shape TEXT columns ────────────────────────
-- These columns carry domain-restricted strings but accept any TEXT today.
-- The application enforces the set, but a mistaken DELETE-then-INSERT or
-- a direct DBA write can leave bad data. NOT VALID + VALIDATE keeps the
-- migration online: the existing rows are scanned in a separate pass that
-- holds only a SHARE UPDATE EXCLUSIVE lock.

ALTER TABLE objects
    ADD CONSTRAINT objects_lock_mode_check
    CHECK (lock_mode IN ('', 'GOVERNANCE', 'COMPLIANCE')) NOT VALID;
ALTER TABLE objects VALIDATE CONSTRAINT objects_lock_mode_check;

ALTER TABLE object_versions
    ADD CONSTRAINT object_versions_lock_mode_check
    CHECK (lock_mode IN ('', 'GOVERNANCE', 'COMPLIANCE')) NOT VALID;
ALTER TABLE object_versions VALIDATE CONSTRAINT object_versions_lock_mode_check;

ALTER TABLE storage_backends
    ADD CONSTRAINT storage_backends_kind_check
    CHECK (kind IN ('aws-s3', 's3-compatible', 'gcs')) NOT VALID;
ALTER TABLE storage_backends VALIDATE CONSTRAINT storage_backends_kind_check;

ALTER TABLE event_subscriptions
    ADD CONSTRAINT event_subscriptions_sink_kind_check
    CHECK (sink_kind IN ('http', 'kafka', 'sqs')) NOT VALID;
ALTER TABLE event_subscriptions VALIDATE CONSTRAINT event_subscriptions_sink_kind_check;

ALTER TABLE buckets
    ADD CONSTRAINT buckets_object_lock_mode_check
    CHECK (object_lock_default_mode IN ('', 'GOVERNANCE', 'COMPLIANCE')) NOT VALID;
ALTER TABLE buckets VALIDATE CONSTRAINT buckets_object_lock_mode_check;

-- ─── G. Future work documented (not in this migration) ─────────────────────
--
-- Partitioning candidates (don't fix today, document for slice 22+):
--
--   * audit_log — append-only, range-purged. Daily/weekly RANGE
--     partitioning by `at` would let DROP PARTITION replace DELETE
--     (no dead-tuple bloat, no VACUUM needed) and keeps recent
--     queries on a single partition.
--   * idempotency_keys — same shape: insert + TTL purge. Hourly/daily
--     partitions on `expires_at`.
--
-- Both partitionings are non-trivial because existing rows must be
-- re-bucketed; deferred to a dedicated migration with downtime planning.
--
-- Row-Level Security (RLS):
--
--   security.enable_rls in config is currently a no-op flag (the
--   policies aren't authored). The right move is to keep tenant
--   isolation at the application layer (Cedar) and add RLS as
--   defence-in-depth in a future migration that authors per-table
--   policies and switches the app role to a non-bypass role.

ANALYZE;

-- +goose Down

ALTER TABLE buckets DROP CONSTRAINT IF EXISTS buckets_object_lock_mode_check;
ALTER TABLE event_subscriptions DROP CONSTRAINT IF EXISTS event_subscriptions_sink_kind_check;
ALTER TABLE storage_backends DROP CONSTRAINT IF EXISTS storage_backends_kind_check;
ALTER TABLE object_versions DROP CONSTRAINT IF EXISTS object_versions_lock_mode_check;
ALTER TABLE objects DROP CONSTRAINT IF EXISTS objects_lock_mode_check;

ALTER TABLE event_subscriptions RESET (fillfactor);
ALTER TABLE multipart_uploads   RESET (fillfactor);
ALTER TABLE operations          RESET (fillfactor);
ALTER TABLE api_keys            RESET (fillfactor);
ALTER TABLE users               RESET (fillfactor);
ALTER TABLE quotas              RESET (fillfactor);
ALTER TABLE objects             RESET (fillfactor);

ALTER TABLE tenants ALTER COLUMN labels   SET STATISTICS -1;
ALTER TABLE objects ALTER COLUMN metadata SET STATISTICS -1;
ALTER TABLE objects ALTER COLUMN tags     SET STATISTICS -1;

DROP INDEX CONCURRENTLY IF EXISTS idx_objects_failed_updated;
DROP INDEX CONCURRENTLY IF EXISTS idx_objects_pending_created;
DROP INDEX CONCURRENTLY IF EXISTS idx_object_versions_delete_markers;
DROP INDEX CONCURRENTLY IF EXISTS idx_operations_terminal_done_at;
DROP INDEX CONCURRENTLY IF EXISTS idx_refresh_tokens_active_expiry;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_refresh_tokens_expiry ON refresh_tokens(expires_at);
DROP INDEX CONCURRENTLY IF EXISTS idx_api_keys_active_expiry;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_tenant_at;
