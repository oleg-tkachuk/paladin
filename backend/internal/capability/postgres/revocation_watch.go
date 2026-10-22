package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/limes/pgstore"
)

// NewRevocationWatcher is pgstore's watcher of the revocations made anywhere —
// any replica, an operator's psql — clearing caches when one is and after
// every reconnect, so a revocation reaches this replica at once rather than
// when its cached answer expires. A cache that is a pgstore.Syncer, as
// limes.RevocationCache is, is also told when the watcher starts and stops
// listening, and serves its longer TTL only in between. It listens on a connection taken from pool
// for good (Hijack): a LISTEN left on a pooled connection would follow it
// back into the pool. onError hears each failure it reconnects after.
func NewRevocationWatcher(pool *pgxpool.Pool, onError func(error), caches ...pgstore.Clearer) pgstore.RevocationWatcher {
	return pgstore.RevocationWatcher{
		Connect: func(ctx context.Context) (*pgx.Conn, error) {
			c, err := pool.Acquire(ctx)
			if err != nil {
				return nil, fmt.Errorf("capability/postgres: revocation watch: %w", err)
			}
			return c.Hijack(), nil
		},
		Caches:  caches,
		OnError: onError,
	}
}
