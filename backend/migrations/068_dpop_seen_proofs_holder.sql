-- +goose Up
-- +goose StatementBegin

-- limes v0.40 counts a DPoP proof id against the share of its holder — the
-- capability the proof was presented with — rather than of the key that
-- signed it, so one holder cannot spend its share by rotating keys. Proof ids
-- expire within the acceptance window and nothing in them outlives the
-- deploy, so the table is recreated empty rather than migrated. No row level
-- security, for the reasons 037 gives.
DROP TABLE dpop_seen_proofs;
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

-- +goose Down
-- +goose StatementBegin
DROP TABLE dpop_seen_proofs;
CREATE TABLE dpop_seen_proofs (
    jkt        text NOT NULL,
    jti        text NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (jkt, jti)
);
CREATE INDEX dpop_seen_proofs_expiry_idx ON dpop_seen_proofs (expires_at);
-- +goose StatementEnd
