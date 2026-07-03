-- +goose Up
-- +goose StatementBegin
-- 055_storage_backend_provider.sql
--
-- Vendor/implementation slug for a storage backend, distinct from `kind`.
-- `kind` (aws-s3 | s3-compatible | gcs) is too coarse: every self-hosted S3
-- (Garage, SeaweedFS, MinIO, Ceph RGW, DigitalOcean Spaces, …) collapses to
-- 's3-compatible'. `provider` records which one, for UI display and any
-- future vendor-specific handling.
--
-- CONFIG-MIRRORED (like kind/endpoint/region, unlike the operator-managed
-- enabled/read_only/maintenance flags): the bootstrap reconciler writes it
-- from storage.backends.<id>.provider. DEFAULT '' backfills existing rows and
-- means "unset" — the UI then falls back to an endpoint heuristic.
ALTER TABLE storage_backends
    ADD COLUMN provider TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE storage_backends
    DROP COLUMN IF EXISTS provider;
-- +goose StatementEnd
