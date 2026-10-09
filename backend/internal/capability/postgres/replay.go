package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
)

// ReplayPurgeBatch bounds how many expired proof ids one purge statement
// deletes, so a backlog drains in short statements rather than one long one.
const ReplayPurgeBatch = 10_000

// ReplayCache is a limes.ReplayCache shared by every replica through
// the dpop_seen_jti table (migration 037): a proof accepted on one replica is
// refused on all the others for as long as it could be accepted at all.
//
// It fails closed. A proof whose id cannot be recorded is refused, because
// accepting it unrecorded would let it be replayed anywhere; the error is
// logged, so an unreachable database is told apart from a replay.
type ReplayCache struct {
	pool *pgxpool.Pool
	log  *zap.Logger
}

var _ limes.ReplayCache = (*ReplayCache)(nil)

// NewReplayCache builds a ReplayCache over pool. Caller owns the pool.
func NewReplayCache(pool *pgxpool.Pool, log *zap.Logger) (*ReplayCache, error) {
	if pool == nil {
		return nil, errors.New("capability/postgres: replay cache: pool required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ReplayCache{pool: pool, log: log}, nil
}

// Seen implements limes.ReplayCache. It records jti until expires and
// reports whether a live record of it already existed. A record that has
// expired but not yet been purged does not count: it is overwritten, as the
// in-process cache forgets an expired id.
func (c *ReplayCache) Seen(ctx context.Context, jti string, expires time.Time) bool {
	const stmt = `
INSERT INTO dpop_seen_jti (jti, expires_at)
VALUES ($1, $2)
ON CONFLICT (jti) DO UPDATE SET expires_at = EXCLUDED.expires_at
    WHERE dpop_seen_jti.expires_at <= now()
RETURNING jti
`
	var recorded string
	err := c.pool.QueryRow(ctx, stmt, jti, expires).Scan(&recorded)
	switch {
	case err == nil:
		return false
	case errors.Is(err, pgx.ErrNoRows):
		return true
	default:
		c.log.Warn("DPoP replay check failed; refusing the proof", zap.Error(err))
		return true
	}
}

// PurgeExpired deletes up to ReplayPurgeBatch proof ids whose proofs can no
// longer be accepted, and reports how many it deleted. Callers loop until it
// returns 0.
func (c *ReplayCache) PurgeExpired(ctx context.Context) (int64, error) {
	const stmt = `
DELETE FROM dpop_seen_jti
WHERE jti IN (
    SELECT jti FROM dpop_seen_jti
    WHERE  expires_at <= now()
    LIMIT  $1
)
`
	tag, err := c.pool.Exec(ctx, stmt, ReplayPurgeBatch)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: purge DPoP proof ids: %w", err)
	}
	return tag.RowsAffected(), nil
}
