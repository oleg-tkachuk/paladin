package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/limes"
)

var _ limes.BiscuitRevocationStore = (*Store)(nil)

// IsBiscuitRevoked implements limes.BiscuitRevocationLookup. Like
// IsRevoked it runs before any tenant is on the context, so it reads under the
// cross-tenant flag, SET LOCAL to its read-only transaction; the ids it looks
// for come from a Biscuit whose signature chain has verified.
func (s *Store) IsBiscuitRevoked(ctx context.Context, revocationIDs [][]byte) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, fmt.Errorf("capability/postgres: is_biscuit_revoked begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.cross_tenant', 'on', true)`); err != nil {
		return false, fmt.Errorf("capability/postgres: is_biscuit_revoked set cross-tenant: %w", err)
	}
	const stmt = `
SELECT EXISTS (
    SELECT 1 FROM capability_biscuit_revocations
    WHERE  revocation_id = ANY($1::bytea[])
);
`
	var revoked bool
	if err := tx.QueryRow(ctx, stmt, revocationIDs).Scan(&revoked); err != nil {
		return false, fmt.Errorf("capability/postgres: is_biscuit_revoked: %w", err)
	}
	return revoked, nil
}

// GetBiscuitRevocation implements limes.BiscuitRevocationStore. The row
// is visible through its capability (038), as RevokeBiscuit's target is.
func (s *Store) GetBiscuitRevocation(ctx context.Context, revocationID []byte) (limes.BiscuitRevocation, error) {
	const stmt = `
SELECT capability_id, revoked_at, reason, actor
FROM   capability_biscuit_revocations
WHERE  revocation_id = $1;
`
	r := limes.BiscuitRevocation{RevocationID: slices.Clone(revocationID)}
	err := s.pool.QueryRow(ctx, stmt, revocationID).Scan(&r.CapabilityID, &r.RevokedAt, &r.Reason, &r.Actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return limes.BiscuitRevocation{}, ErrNotFound
	}
	if err != nil {
		return limes.BiscuitRevocation{}, fmt.Errorf("capability/postgres: get biscuit revocation: %w", err)
	}
	r.RevokedAt = r.RevokedAt.UTC()
	return r, nil
}

// RevokeBiscuit implements limes.BiscuitRevocationStore. As in Revoke,
// the capability id is read from capability_records in the same statement, so
// a caller that cannot see the capability gets ErrNotFound and writes nothing.
func (s *Store) RevokeBiscuit(ctx context.Context, args limes.RevokeBiscuitRequest) error {
	if len(args.RevocationID) == 0 {
		return fmt.Errorf("%w: capability/postgres: revoke biscuit: revocation id required", limes.ErrInvalidRequest)
	}
	const stmt = `
WITH target AS (
    SELECT id FROM capability_records WHERE id = $1
),
inserted AS (
    INSERT INTO capability_biscuit_revocations (revocation_id, capability_id, reason, actor)
    SELECT $2, id, $3, $4 FROM target
    ON CONFLICT (revocation_id) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM target;
`
	var visible int64
	if err := s.pool.QueryRow(ctx, stmt, args.CapabilityID, args.RevocationID, args.Reason, args.Actor).Scan(&visible); err != nil {
		return fmt.Errorf("capability/postgres: revoke biscuit: %w", err)
	}
	if visible == 0 {
		return ErrNotFound
	}
	return nil
}
