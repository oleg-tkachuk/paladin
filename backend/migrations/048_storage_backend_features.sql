-- +goose Up

-- What TestBackend's probe found for each S3 feature Paladin uses (ADR-0026).
-- An observation, not configuration: like storage_backend_health it sits
-- outside storage_backends, so recording a probe never moves the backend's
-- resource_version. Each probe replaces every row of its backend.
--
-- `feature` is a catalog name from internal/storage/features. It carries no
-- CHECK: a row for a feature a later release dropped is ignored on read, and
-- a CHECK would make adding a feature a migration.
CREATE TABLE storage_backend_features (
    backend_id uuid NOT NULL REFERENCES storage_backends(id) ON DELETE CASCADE,
    feature    text NOT NULL,
    support    text NOT NULL CHECK (support IN ('supported', 'unsupported', 'unknown')),
    message    text NOT NULL DEFAULT '',
    checked_at timestamptz NOT NULL,
    PRIMARY KEY (backend_id, feature)
);

-- +goose Down
DROP TABLE storage_backend_features;
