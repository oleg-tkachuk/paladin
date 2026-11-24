-- +goose Up
-- +goose StatementBegin

-- The per-bucket replication watermark is replaced by per-object state
-- (object_replicas, 119; ADR-0050). Nothing reads it any more.
DROP TABLE replication_state;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE replication_state (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id  uuid NOT NULL UNIQUE REFERENCES buckets(id) ON DELETE CASCADE,
    watermark  timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
