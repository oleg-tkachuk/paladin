-- +goose Up
-- +goose StatementBegin
-- 041_audit_log_partition.sql
--
-- Convert audit_log into a RANGE-partitioned table on `at`, one partition
-- per month.
--
-- WHY. audit_log is append-only with TTL retention. Retention today is a
-- bounded ctid-batched DELETE (worker.AuditLogPurger); the deleted tuples
-- still need VACUUM to reclaim, and a long-overdue first sweep churns WAL.
-- RANGE partitioning lets retention become DROP PARTITION — O(1), zero dead
-- tuples, zero VACUUM. The worker.PartitionMaintainer drops whole months
-- past the TTL and pre-creates upcoming ones. Partitioning is core Postgres
-- (no extension), so this keeps the "runs on any managed Postgres tier"
-- property the worker-form purgers were chosen for.
--
-- COST. This is a TABLE REWRITE. Postgres requires the partition key to be
-- part of every UNIQUE / PRIMARY KEY, so the PK widens entry_id ->
-- (entry_id, at). The new parent is built, existing rows are copied into
-- it, the old table is dropped, and the new one is renamed into place — all
-- under ACCESS EXCLUSIVE inside this one transaction. Run in a maintenance
-- window (docs/runbooks/partition-audit-idempotency.md). entry_id stays
-- effectively unique (app-generated UUIDv7); only the DB-enforced guarantee
-- widens to (entry_id, at), which is sound for an append-only log.
--
-- GRANULARITY = monthly. Audit retention is measured in months, so a month
-- is the smallest drop unit that still keeps the partition count tiny. If a
-- real deploy shows very high daily volume, switch the maintainer to weekly
-- — the partition Period is the only knob (tracked in BACKLOG).

CREATE TABLE audit_log_part (
    entry_id        UUID NOT NULL,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_subject   TEXT NOT NULL,
    actor_tenant_id UUID,
    actor_audience  TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_name   TEXT NOT NULL,
    request_id      TEXT,
    source_ip       TEXT,
    before_json     BYTEA,
    after_json      BYTEA,
    error_message   TEXT,
    capability_id   UUID,
    PRIMARY KEY (entry_id, at)
) PARTITION BY RANGE (at);
-- +goose StatementEnd

-- Catch-all so an insert whose `at` falls outside every monthly partition
-- (clock skew, or a gap before the maintainer's first tick) never fails.
-- The AuditLogPurger DELETE backstop sweeps any stragglers that land here.
CREATE TABLE audit_log_default PARTITION OF audit_log_part DEFAULT;

-- One monthly partition per month spanned by existing rows, plus the
-- current month and two ahead — so the copy below routes every row into a
-- real month (nothing falls to default) and new writes have a home before
-- the maintainer runs.
-- +goose StatementBegin
DO $$
DECLARE
    lo   date;
    hi   date;
    m    date;
    part text;
BEGIN
    SELECT date_trunc('month', COALESCE(min(at), now()))::date INTO lo FROM audit_log;
    hi := (date_trunc('month', now()) + interval '3 months')::date;
    m := lo;
    WHILE m < hi LOOP
        part := 'audit_log_' || to_char(m, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log_part '
            'FOR VALUES FROM (%L) TO (%L)',
            part, m::text, (m + interval '1 month')::date::text);
        m := (m + interval '1 month')::date;
    END LOOP;
END $$;
-- +goose StatementEnd

INSERT INTO audit_log_part (
    entry_id, at, actor_subject, actor_tenant_id, actor_audience, action,
    resource_name, request_id, source_ip, before_json, after_json,
    error_message, capability_id)
SELECT
    entry_id, at, actor_subject, actor_tenant_id, actor_audience, action,
    resource_name, request_id, source_ip, before_json, after_json,
    error_message, capability_id
FROM audit_log;

DROP TABLE audit_log;
ALTER TABLE audit_log_part RENAME TO audit_log;
ALTER INDEX audit_log_part_pkey RENAME TO audit_log_pkey;

-- Indexes — created on the partitioned parent, propagated to every
-- partition (existing and future). Names match the pre-partition schema so
-- query plans and the reaper read identically. Non-CONCURRENT is fine: we
-- hold the maintenance window and the data was just loaded.
CREATE INDEX idx_audit_log_at ON audit_log (at DESC);
CREATE INDEX idx_audit_log_actor ON audit_log (actor_subject, at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_name, at DESC);
CREATE INDEX idx_audit_log_tenant_at ON audit_log (actor_tenant_id, at DESC)
    WHERE actor_tenant_id IS NOT NULL;
CREATE INDEX idx_audit_log_capability ON audit_log (capability_id, at DESC)
    WHERE capability_id IS NOT NULL;
CREATE INDEX idx_audit_log_action_at ON audit_log (action text_pattern_ops, at DESC);

-- RLS — re-stamped on the new table (dropping the old table dropped its
-- policy). Identical to migration 024_rls_extension.sql.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_write_isolation ON audit_log
    FOR ALL
    TO PUBLIC
    USING (true)
    WITH CHECK (
        paladin_session_tenant_id() IS NULL
        OR actor_tenant_id IS NULL
        OR actor_tenant_id = paladin_session_tenant_id()
    );

-- +goose Down
-- +goose StatementBegin
-- Irreversible by policy (forward-only migrations; migrations/CONVENTIONS.md).
-- Un-partitioning is itself a table rewrite; refuse rather than silently
-- corrupt. Restore from a pre-migration backup if you must roll back.
DO $$
BEGIN
    RAISE EXCEPTION 'migration 041 (audit_log partitioning) is forward-only; restore from backup to revert';
END $$;
-- +goose StatementEnd
