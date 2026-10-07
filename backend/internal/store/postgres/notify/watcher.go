// Package notify turns a Postgres LISTEN/NOTIFY channel into a callback: a
// change committed anywhere — any replica, an operator's psql — reaches every
// replica's cache at once instead of when its entries expire.
package notify

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// Re-LISTEN backoff after a dropped connection: small first so a blip recovers
// fast, capped so a long outage does not hot-loop the pool.
const (
	backoffInitial = 100 * time.Millisecond
	backoffMax     = 5 * time.Second
)

// Watcher calls onNotify for every notification on one channel.
//
// It holds one pooled connection for its whole life, so whoever starts it
// must cancel its context before closing the pool.
type Watcher struct {
	pool    *pgxpool.Pool
	channel string
	// onNotify runs once per notification, and once after every reconnect:
	// notifications sent while the connection was down are lost, so a
	// reconnect is treated as "something may have changed".
	onNotify   func()
	reconnects atomic.Uint64
}

// New builds a watcher that calls onNotify — typically a cache's Clear —
// whenever channel is notified. The channel name is an identifier the
// caller owns, never input.
func New(pool *pgxpool.Pool, channel string, onNotify func()) *Watcher {
	return &Watcher{pool: pool, channel: channel, onNotify: onNotify}
}

// Reconnects reports how many times the watcher re-established its LISTEN.
// A climbing value means changes are falling back to the cache TTL between
// drops.
func (w *Watcher) Reconnects() uint64 { return w.reconnects.Load() }

// Start issues the first LISTEN synchronously, so a misconfiguration (a
// missing grant, an unreachable database) fails boot instead of silently
// degrading to TTL-only propagation, then watches in the background until ctx
// is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	conn, err := w.listen(ctx)
	if err != nil {
		return err
	}
	go w.loop(ctx, conn)
	return nil
}

func (w *Watcher) listen(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("notify: %s watch acquire: %w", w.channel, err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+w.channel); err != nil {
		conn.Release()
		return nil, fmt.Errorf("notify: %s watch listen: %w", w.channel, err)
	}
	return conn, nil
}

func (w *Watcher) loop(ctx context.Context, conn *pgxpool.Conn) {
	for {
		err := w.consume(ctx, conn)
		conn.Release()
		if ctx.Err() != nil {
			return
		}
		logger.FromContext(ctx).Warn("notify watch dropped; reconnecting", zap.String("channel", w.channel), zap.Error(err))

		conn = w.relisten(ctx)
		if conn == nil {
			return
		}
		w.reconnects.Add(1)
		// Anything announced during the gap went unheard.
		w.onNotify()
	}
}

func (w *Watcher) consume(ctx context.Context, conn *pgxpool.Conn) error {
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		w.onNotify()
	}
}

func (w *Watcher) relisten(ctx context.Context) *pgxpool.Conn {
	backoff := backoffInitial
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if conn, err := w.listen(ctx); err == nil {
			return conn
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}
