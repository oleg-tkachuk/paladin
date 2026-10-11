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
// the dpop_seen_proofs table (migration 060): a proof accepted on one replica
// is refused on all the others for as long as it could be accepted at all.
// Each key holds at most limes.DefaultReplayEntriesPerKey live proof ids, so
// one key cannot fill the store and have every other key's proofs refused.
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

// Seen implements limes.ReplayCache. It records the proof id jti of the key
// with thumbprint jkt until expires, and reports whether a live record of it
// already existed. A record that has expired but not yet been purged does not
// count: it is overwritten, as the in-process cache forgets an expired id. A
// key past its share records nothing and is answered as a replay.
func (c *ReplayCache) Seen(ctx context.Context, jkt, jti string, expires time.Time) bool {
	const stmt = `
INSERT INTO dpop_seen_proofs (jkt, jti, expires_at)
SELECT $1, $2, $3
WHERE (SELECT count(*) FROM dpop_seen_proofs WHERE jkt = $1 AND expires_at > now()) < $4
ON CONFLICT (jkt, jti) DO UPDATE SET expires_at = EXCLUDED.expires_at
    WHERE dpop_seen_proofs.expires_at <= now()
RETURNING jti
`
	var recorded string
	err := c.pool.QueryRow(ctx, stmt, jkt, jti, expires, limes.DefaultReplayEntriesPerKey).Scan(&recorded)
	switch {
	case err == nil:
		return false
	case errors.Is(err, pgx.ErrNoRows):
		c.logIfOverShare(ctx, jkt, jti)
		return true
	default:
		c.log.Warn("DPoP replay check failed; refusing the proof", zap.Error(err))
		return true
	}
}

// logIfOverShare tells a key refused for being past its share from a proof
// replayed: both are refused alike, but only the first says that a key is
// spending proofs faster than they expire — a client looping, or one key
// trying to crowd the store. The extra read runs on the refusal path only.
func (c *ReplayCache) logIfOverShare(ctx context.Context, jkt, jti string) {
	const stmt = `
SELECT EXISTS (SELECT 1 FROM dpop_seen_proofs WHERE jkt = $1 AND jti = $2 AND expires_at > now())
`
	var replayed bool
	if err := c.pool.QueryRow(ctx, stmt, jkt, jti).Scan(&replayed); err != nil || replayed {
		return
	}
	c.log.Warn("DPoP proof refused: the key is past its share of the replay store",
		zap.String("jkt", jkt), zap.Int("share", limes.DefaultReplayEntriesPerKey))
}

// PurgeExpired deletes up to ReplayPurgeBatch proof ids whose proofs can no
// longer be accepted, and reports how many it deleted. Callers loop until it
// returns 0.
func (c *ReplayCache) PurgeExpired(ctx context.Context) (int64, error) {
	const stmt = `
DELETE FROM dpop_seen_proofs
WHERE (jkt, jti) IN (
    SELECT jkt, jti FROM dpop_seen_proofs
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
