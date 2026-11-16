-- +goose Up
-- +goose StatementBegin

-- A tenant's slug never changes: Cedar names the tenant by it, and so do
-- tokens, policies and every `tenants/<slug>` a client holds, so renaming one
-- was withdrawn from the API. The rotations this table recorded can no longer
-- happen, and nothing reads it.
DROP TABLE tenant_slug_history;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE tenant_slug_history (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    old_slug   text NOT NULL,
    new_slug   text NOT NULL,
    renamed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tenant_slug_history_tenant_idx ON tenant_slug_history (tenant_id);
CREATE INDEX tenant_slug_history_old_slug_idx ON tenant_slug_history (old_slug);

-- +goose StatementEnd
