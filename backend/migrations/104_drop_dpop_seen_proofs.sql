-- +goose Up
-- +goose StatementBegin

-- The DPoP replay cache is pgstore's (limes.dpop_seen_proofs, pgstore
-- migration 023), so Paladin's own table goes. Proof ids expire within the
-- acceptance window, so nothing is carried over.
DROP TABLE dpop_seen_proofs;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE dpop_seen_proofs (
    jkt        text NOT NULL,
    jti        text NOT NULL,
    holder     text NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (jkt, jti)
);
CREATE INDEX dpop_seen_proofs_expiry_idx ON dpop_seen_proofs (expires_at);
CREATE INDEX dpop_seen_proofs_holder_idx ON dpop_seen_proofs (holder, expires_at);
-- +goose StatementEnd
