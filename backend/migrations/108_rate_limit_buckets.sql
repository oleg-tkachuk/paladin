-- +goose Up
-- +goose StatementBegin

-- One table for both request rate limits (ADR-0046). Each replica counts in
-- memory and adds what it counted here once per sync interval, then reads the
-- totals back, so this takes a write per replica per interval instead of one
-- per request. The key names the subject: "tenant:<uuid>" or "token:<uuid>".
--
-- UNLOGGED: a count is worth a minute. A crash empties the table, which lifts
-- each limit for at most one window; writing it to the WAL on every sync
-- would buy nothing a minute later.
--
-- No tenant_id and no policy: the sync writes every live tenant's and
-- token's counts in one statement from a connection with no tenant in its
-- session. The rows hold counts, not tenant data. A deleted tenant's rows
-- leave with the sweep, within minutes, rather than by cascade.
CREATE UNLOGGED TABLE rate_limit_buckets (
    key          text        NOT NULL,
    bucket_start timestamptz NOT NULL,
    count        bigint      NOT NULL,
    PRIMARY KEY (key, bucket_start)
);

-- The sweep deletes by age across every key; the primary key is key-first.
CREATE INDEX rate_limit_buckets_sweep_idx ON rate_limit_buckets (bucket_start);

DROP TABLE tenant_rate_buckets;
DROP TABLE api_token_rate_buckets;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE api_token_rate_buckets (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_id     uuid NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0,
    UNIQUE (token_id, bucket_start)
);

CREATE TABLE tenant_rate_buckets (
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, bucket_start)
);
CREATE INDEX tenant_rate_buckets_sweep_idx ON tenant_rate_buckets (bucket_start);
ALTER TABLE tenant_rate_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_rate_buckets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_rate_buckets FOR ALL
    USING (tenant_id = paladin_session_tenant_id()
           OR paladin_session_cross_tenant())
    WITH CHECK (tenant_id = paladin_session_tenant_id());

DROP TABLE rate_limit_buckets;

-- +goose StatementEnd
