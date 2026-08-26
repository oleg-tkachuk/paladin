-- +goose Up
-- +goose StatementBegin

-- Shared buckets for the per-tenant request rate limit.
--
-- The limiter was an in-memory token bucket per process, so the ceiling a
-- tenant actually got was replicas × requests_per_second — 600/s at the
-- chart's own default of two api replicas, where the config said 300. The
-- number an operator set did not mean what it says, and nothing surfaced the
-- discrepancy: no error, just a looser limit.
--
-- Same shape and same sliding-window algorithm as api_token_rate_buckets,
-- which solved this for per-token limits at the schema baseline. One-minute
-- fixed buckets; the effective trailing-60s count is
--
--   current + previous * (1 - elapsed_in_current / 60)
--
-- The policy is applied in 011, not here: this migration shipped claiming one
-- could not work, on the theory that the limiter runs before any tenant scope
-- exists. It does not — EnableRLS sets the session tenant in PrepareConn, on
-- every connection checkout, and the limiter runs after the auth
-- interceptors.

CREATE TABLE tenant_rate_buckets (
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, bucket_start)
);

-- The sweep deletes by age across every tenant, so it needs bucket_start
-- leading; the primary key above is tenant-first and cannot serve it.
CREATE INDEX tenant_rate_buckets_sweep_idx ON tenant_rate_buckets (bucket_start);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS tenant_rate_buckets;

-- +goose StatementEnd
