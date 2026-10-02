package postgres

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// RevokedChannel is the LISTEN/NOTIFY channel migration 033 announces every
// revoking statement on.
const RevokedChannel = "capability_revoked"

// Re-LISTEN backoff after a dropped connection: small first so a blip recovers
// fast, capped so a long outage does not hot-loop the pool.
const (
	revocationWatchBackoffInitial = 100 * time.Millisecond
	revocationWatchBackoffMax     = 5 * time.Second
)

// RevocationWatcher turns revocations made anywhere — any replica, an
// operator's psql — into an immediate cache flush on this one. Without it a
// revocation reached other replicas only when their cached answer expired.
//
// It holds one pooled connection for its whole life, so whoever starts it
// must cancel its context before closing the pool.
type RevocationWatcher struct {
	pool *pgxpool.Pool
	// onRevoked runs once per notification, and once after every reconnect:
	// notifications sent while the connection was down are lost, so a
	// reconnect is treated as "something may have been revoked".
	onRevoked  func()
	reconnects atomic.Uint64
}

// NewRevocationWatcher builds a watcher that calls onRevoked — typically
// CachedRevocationChecker.Clear — whenever a capability is revoked.
func NewRevocationWatcher(pool *pgxpool.Pool, onRevoked func()) *RevocationWatcher {
	return &RevocationWatcher{pool: pool, onRevoked: onRevoked}
}

// Reconnects reports how many times the watcher re-established its LISTEN.
// A climbing value means revocations are falling back to the cache TTL
// between drops.
func (w *RevocationWatcher) Reconnects() uint64 { return w.reconnects.Load() }

// Start issues the first LISTEN synchronously, so a misconfiguration (a
// missing grant, an unreachable database) fails boot instead of silently
// degrading to TTL-only propagation, then watches in the background until ctx
// is cancelled.
func (w *RevocationWatcher) Start(ctx context.Context) error {
	conn, err := w.listen(ctx)
	if err != nil {
		return err
	}
	go w.loop(ctx, conn)
	return nil
}

func (w *RevocationWatcher) listen(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: revocation watch acquire: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+RevokedChannel); err != nil {
		conn.Release()
		return nil, fmt.Errorf("capability/postgres: revocation watch listen: %w", err)
	}
	return conn, nil
}

func (w *RevocationWatcher) loop(ctx context.Context, conn *pgxpool.Conn) {
	for {
		err := w.consume(ctx, conn)
		conn.Release()
		if ctx.Err() != nil {
			return
		}
		logger.FromContext(ctx).Warn("capability revocation watch dropped; reconnecting", zap.Error(err))

		conn = w.relisten(ctx)
		if conn == nil {
			return
		}
		w.reconnects.Add(1)
		// Anything revoked during the gap went unannounced.
		w.onRevoked()
	}
}

func (w *RevocationWatcher) consume(ctx context.Context, conn *pgxpool.Conn) error {
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		w.onRevoked()
	}
}

func (w *RevocationWatcher) relisten(ctx context.Context) *pgxpool.Conn {
	backoff := revocationWatchBackoffInitial
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if conn, err := w.listen(ctx); err == nil {
			return conn
		}
		if backoff *= 2; backoff > revocationWatchBackoffMax {
			backoff = revocationWatchBackoffMax
		}
	}
}
