-- +goose Up
-- +goose StatementBegin

-- Replication watermark — per (backend, bucket) high-water mark of the
-- newest object_committed_at the worker has successfully replicated.
-- Survives process restarts, so the worker doesn't re-scan the lookback
-- window on every cold-boot.
CREATE TABLE IF NOT EXISTS replication_state (
    backend_id    TEXT NOT NULL,
    bucket_name   TEXT NOT NULL,
    watermark     TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (backend_id, bucket_name),
    FOREIGN KEY (backend_id, bucket_name)
        REFERENCES buckets(backend_id, bucket_name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_replication_state_updated
    ON replication_state(updated_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replication_state;
-- +goose StatementEnd
