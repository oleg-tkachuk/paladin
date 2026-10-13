package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes/dpop"
)

// ReplayPurgeBatch bounds how many expired proof ids one purge statement
// deletes, so a backlog drains in short statements rather than one long one.
const ReplayPurgeBatch = 10_000

// ReplayCache is a dpop.ReplayCache shared by every replica through the
// dpop_seen_proofs table (migration 068): a proof accepted on one replica is
// refused on all the others for as long as it could be accepted at all. Each
// holder — the capability a proof is presented with — holds at most
// EntriesPerHolder live proof ids, whatever keys signed them, so one holder
// cannot fill the store and have every other holder's proofs refused.
//
// It fails closed. A proof whose id cannot be recorded is refused: Seen
// returns the error and the verifier refuses the proof with it, so an
// unreachable database is told apart from a replay.
type ReplayCache struct {
	pool *pgxpool.Pool
	log  *zap.Logger
	// now is the clock a recorded id expires by.
	now func() time.Time
	// perHolder is the live proof ids one holder may have recorded.
	perHolder int
}

var _ dpop.ReplayCache = (*ReplayCache)(nil)

// ReplayCacheConfig configures a ReplayCache.
type ReplayCacheConfig struct {
	// Pool is the database; the caller owns it.
	Pool *pgxpool.Pool
	// Log is optional: nil installs a no-op.
	Log *zap.Logger
	// Now is the clock a recorded id expires by. Optional: the wall clock.
	Now func() time.Time
	// EntriesPerHolder is the live proof ids one holder may have recorded.
	// Zero: dpop.DefaultReplayEntriesPerHolder; negative is refused.
	EntriesPerHolder int
}

// NewReplayCache builds a ReplayCache.
func NewReplayCache(cfg ReplayCacheConfig) (*ReplayCache, error) {
	if cfg.Pool == nil {
		return nil, errors.New("capability/postgres: replay cache: pool required")
	}
	if cfg.EntriesPerHolder < 0 {
		return nil, fmt.Errorf("capability/postgres: replay cache: entries per holder %d is negative", cfg.EntriesPerHolder)
	}
	c := &ReplayCache{pool: cfg.Pool, log: cfg.Log, now: cfg.Now, perHolder: cfg.EntriesPerHolder}
	if c.log == nil {
		c.log = zap.NewNop()
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.perHolder == 0 {
		c.perHolder = dpop.DefaultReplayEntriesPerHolder
	}
	return c, nil
}

// seenQuery records a proof id unless a live record of it exists or its
// holder has used its share. A record that has expired but not yet been
// purged does not count: it is overwritten, as the in-process cache forgets
// an expired id. No row back means the proof is refused.
const seenQuery = `
INSERT INTO dpop_seen_proofs (jkt, jti, holder, expires_at)
SELECT $1, $2, $3, $4
WHERE (SELECT count(*) FROM dpop_seen_proofs WHERE holder = $3 AND expires_at > $5) < $6
ON CONFLICT (jkt, jti) DO UPDATE SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at
    WHERE dpop_seen_proofs.expires_at <= $5
RETURNING jti
`

// Seen implements dpop.ReplayCache. It records e until e.ExpiresAt, and
// reports whether its id was already recorded; a holder past its share
// records nothing and is answered as a replay.
func (c *ReplayCache) Seen(ctx context.Context, e dpop.ReplayEntry) (bool, error) {
	now := c.now()
	var recorded string
	err := c.pool.QueryRow(ctx, seenQuery, e.JKT, e.JTI, e.Holder, e.ExpiresAt, now, c.perHolder).Scan(&recorded)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, pgx.ErrNoRows):
		c.logIfOverShare(ctx, e, now)
		return true, nil
	default:
		return true, fmt.Errorf("capability/postgres: DPoP replay check: %w", err)
	}
}

// logIfOverShare tells a holder refused for being past its share from a proof
// replayed: both are refused alike, but only the first says that a holder is
// spending proofs faster than they expire — a client looping, or one holder
// trying to crowd the store. The extra read runs on the refusal path only.
func (c *ReplayCache) logIfOverShare(ctx context.Context, e dpop.ReplayEntry, now time.Time) {
	const stmt = `
SELECT EXISTS (SELECT 1 FROM dpop_seen_proofs WHERE jkt = $1 AND jti = $2 AND expires_at > $3)
`
	var replayed bool
	if err := c.pool.QueryRow(ctx, stmt, e.JKT, e.JTI, now).Scan(&replayed); err != nil || replayed {
		return
	}
	c.log.Warn("DPoP proof refused: the holder is past its share of the replay store",
		zap.String("holder", e.Holder), zap.Int("share", c.perHolder))
}

// PurgeExpired deletes up to ReplayPurgeBatch proof ids whose proofs can no
// longer be accepted, and reports how many it deleted. Callers loop until it
// returns 0.
func (c *ReplayCache) PurgeExpired(ctx context.Context) (int64, error) {
	const stmt = `
DELETE FROM dpop_seen_proofs
WHERE (jkt, jti) IN (
    SELECT jkt, jti FROM dpop_seen_proofs
    WHERE  expires_at <= $2
    LIMIT  $1
)
`
	tag, err := c.pool.Exec(ctx, stmt, ReplayPurgeBatch, c.now())
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: purge DPoP proof ids: %w", err)
	}
	return tag.RowsAffected(), nil
}
