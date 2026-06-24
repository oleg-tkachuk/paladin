-- +goose Up
-- +goose StatementBegin
-- 042_idempotency_keys_partition.sql
--
-- Convert idempotency_keys into a RANGE-partitioned table on `expires_at`,
-- one partition per day.
--
-- WHY. Same shape as audit_log (041): insert + TTL purge, where the
-- worker.IdempotencyKeyPurger DELETE leaves dead tuples that bloat the hot
-- GetIdempotencyKey unique index. DROP PARTITION reclaims a whole expired
-- day at once — no VACUUM, no index bloat.
--
-- COST. TABLE REWRITE under ACCESS EXCLUSIVE, run in the same maintenance
-- window as 041 (docs/runbooks/partition-audit-idempotency.md). The PK
-- widens (tenant_id, method, key) -> (tenant_id, method, key, expires_at)
-- to satisfy the partition-key-in-every-unique-constraint rule. Two keys
-- that differ only in expires_at cannot collide in practice — a given
-- (tenant, method, key) is written once with one TTL — so widening the PK
-- does not weaken the idempotency guarantee.
--
-- GRANULARITY = daily. Idempotency-key TTLs are hours to a few days, so a
-- day is the granularity at which DROP PARTITION reclaims promptly without
-- waiting a month. The maintainer keeps ~8 days of partitions ahead and
-- drops days fully past the TTL.

CREATE TABLE idempotency_keys_part (
    tenant_id    UUID NOT NULL,
    method       TEXT NOT NULL,
    key          TEXT NOT NULL,
    response     BYTEA NOT NULL,
    response_sha BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    -- Preserve the tenant FK (migration 004, ON DELETE CASCADE). Postgres
    -- supports an outbound FK from a partitioned table (PG12+). Named to
    -- match so it survives the rename transparently. The PK's leading
    -- tenant_id column serves the cascade lookup — no extra index.
    CONSTRAINT idempotency_keys_tenant_id_fkey
        FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, method, key, expires_at)
) PARTITION BY RANGE (expires_at);
-- +goose StatementEnd

-- Catch-all so a write whose expires_at falls outside the daily window
-- never fails; the IdempotencyKeyPurger DELETE backstop sweeps it.
CREATE TABLE idempotency_keys_default PARTITION OF idempotency_keys_part DEFAULT;

-- Daily partitions covering recent expirations and the forward TTL window
-- [now-2d, now+8d). Already-expired rows older than that fall to default
-- (they are due for purge anyway); live keys (expires_at in now..now+TTL)
-- route into a real day.
-- +goose StatementBegin
DO $$
DECLARE
    lo   date := (now() - interval '2 days')::date;
    hi   date := (now() + interval '8 days')::date;
    d    date;
    part text;
BEGIN
    d := lo;
    WHILE d < hi LOOP
        part := 'idempotency_keys_' || to_char(d, 'YYYYMMDD');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF idempotency_keys_part '
            'FOR VALUES FROM (%L) TO (%L)',
            part, d::text, (d + interval '1 day')::date::text);
        d := (d + interval '1 day')::date;
    END LOOP;
END $$;
-- +goose StatementEnd

INSERT INTO idempotency_keys_part (
    tenant_id, method, key, response, response_sha, created_at, expires_at)
SELECT
    tenant_id, method, key, response, response_sha, created_at, expires_at
FROM idempotency_keys;

DROP TABLE idempotency_keys;
ALTER TABLE idempotency_keys_part RENAME TO idempotency_keys;
ALTER INDEX idempotency_keys_part_pkey RENAME TO idempotency_keys_pkey;

-- Expiry index — propagated to every partition. Matches migration 001.
CREATE INDEX idx_idempotency_keys_expiry ON idempotency_keys (expires_at);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'migration 042 (idempotency_keys partitioning) is forward-only; restore from backup to revert';
END $$;
-- +goose StatementEnd
