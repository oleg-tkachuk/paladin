-- +goose Up
-- +goose StatementBegin
-- 034_tenant_default_bindings.sql
--
-- Phase 3 of canonical-resource-names plan, brought forward to make
-- tenant creation pick a (backend, bucket) up-front. Without this
-- table a tenant is decoupled from any physical S3 location until
-- an ObjectKey is bound; with it, the operator answers "where do
-- this tenant's objects live?" at the moment of tenant birth.
--
-- One row per tenant. backend_id + bucket_name reference the
-- composite PK on `buckets`, so deleting a bucket out from under a
-- tenant requires explicit rebind first (RESTRICT). Cascading on
-- tenant deletion is fine — the tenant's gone, the binding has no
-- referent.
--
-- This is the "B alias" sub-table from the plan: future bare-name
-- requests (`objectKeys/{ok}`) use this row to resolve to the
-- canonical (backend, bucket, tenant_id, objectKey) shape.

CREATE TABLE tenant_default_bindings (
    tenant_id   uuid        PRIMARY KEY
                            REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    backend_id  text        NOT NULL,
    bucket_name text        NOT NULL,
    set_at      timestamptz NOT NULL DEFAULT now(),
    set_by      text        NOT NULL DEFAULT '',
    FOREIGN KEY (backend_id, bucket_name)
        REFERENCES buckets(backend_id, bucket_name) ON DELETE RESTRICT
);

-- The composite FK on buckets needs an index on (backend_id, bucket_name)
-- to keep DELETE on buckets cheap. The buckets table already has its
-- own composite PK so the lookup is free; no extra index here.

CREATE INDEX idx_tenant_default_bindings_bucket
    ON tenant_default_bindings (backend_id, bucket_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tenant_default_bindings_bucket;
DROP TABLE IF EXISTS tenant_default_bindings;
-- +goose StatementEnd
