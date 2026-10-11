-- +goose Up
-- +goose StatementBegin

-- DPoP proof ids, now per key. limes v0.6.0 records a proof id with its key's
-- thumbprint and gives each key a bounded share of the store, so one holder of
-- a key-bound token cannot fill it and have every other client's proofs
-- refused as replays. A proof id is unique only within its key from now on.
--
-- Replaces dpop_seen_jti, whose rows are proof ids that expire within the
-- acceptance window: nothing in them outlives the deploy. No row level
-- security, for the reasons 037 gives. A new, empty table, so its index is
-- built in the same transaction.
CREATE TABLE dpop_seen_proofs (
    jkt        text NOT NULL,
    jti        text NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (jkt, jti)
);
CREATE INDEX dpop_seen_proofs_expiry_idx ON dpop_seen_proofs (expires_at);

DROP TABLE dpop_seen_jti;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE dpop_seen_jti (
    jti        text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX dpop_seen_jti_expiry_idx ON dpop_seen_jti (expires_at);
DROP TABLE dpop_seen_proofs;
-- +goose StatementEnd
