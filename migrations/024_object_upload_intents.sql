-- +goose Up
-- +goose StatementBegin

-- object_upload_intents holds the server-generated upload intent produced by
-- UploadObject. A row here represents "we handed out a presigned URL for this
-- key" — no object exists in the `objects` table yet. When the client calls
-- CompleteObject and PALADIN verifies the S3 HEAD succeeded, the intent row is
-- consumed (deleted) inside the same transaction that INSERTs the final
-- `objects` row with status='complete'. If the client never completes, the
-- intent expires and the Reaper removes it; the `objects` table never sees
-- a failed or abandoned upload.
CREATE TABLE object_upload_intents (
    id              UUID        PRIMARY KEY,
    tenant_id       TEXT        NOT NULL,
    bucket          TEXT        NOT NULL,
    object_key      TEXT        NOT NULL,
    category        TEXT        NOT NULL,
    subpath         TEXT        NULL,
    content_type    TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL,
    labels          JSONB       NOT NULL DEFAULT '{}',
    tags            JSONB       NOT NULL DEFAULT '{}',
    external_ref    TEXT        NULL,
    idempotency_key TEXT        NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,

    CONSTRAINT uq_object_upload_intents_tenant_key UNIQUE (tenant_id, bucket, object_key),
    CONSTRAINT chk_object_upload_intents_category CHECK (
        category ~ '^[a-z0-9][a-z0-9_-]{0,62}$'
    ),
    CONSTRAINT chk_object_upload_intents_subpath CHECK (
        subpath IS NULL OR (
            length(subpath) <= 256
            AND subpath NOT LIKE '%..%'
            AND subpath NOT LIKE '//%'
            AND subpath !~ '^/'
            AND subpath !~ '/$'
        )
    )
);

-- RLS: tenants only see their own intents.
ALTER TABLE object_upload_intents ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_object_upload_intents ON object_upload_intents
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Primary access pattern: Reaper scans for expired intents globally.
CREATE INDEX idx_object_upload_intents_expires_at
    ON object_upload_intents (expires_at);

-- Secondary access pattern: handler resolves (tenant, bucket, key) -> intent
-- during CompleteObject. Covered by the UNIQUE constraint above.

-- Optional idempotency lookup when clients retry UploadObject.
CREATE INDEX idx_object_upload_intents_idempotency
    ON object_upload_intents (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_object_upload_intents_idempotency;
DROP INDEX IF EXISTS idx_object_upload_intents_expires_at;
DROP POLICY IF EXISTS tenant_isolation_object_upload_intents ON object_upload_intents;
DROP TABLE IF EXISTS object_upload_intents;
-- +goose StatementEnd
