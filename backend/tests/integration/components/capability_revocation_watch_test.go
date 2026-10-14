//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/pgstore"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// watchRevocations runs the server's revocation watcher over pool, clearing
// caches, until ctx ends or the test does; a failure it reconnects after
// fails the test.
func watchRevocations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, caches ...pgstore.Clearer) {
	t.Helper()
	ctx, stop := context.WithCancel(ctx)
	w := capstore.NewRevocationWatcher(pool, func(err error) {
		if ctx.Err() == nil {
			t.Errorf("revocation watch: %v", err)
		}
	}, caches...)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
}

// A revocation committed anywhere must reach a replica's cache by
// notification, not by waiting out the TTL. The cache here holds answers for
// an hour, so only the watcher can make the revoked capability — and its
// child, cached under its own id — fail promptly.
func TestRevocationNotificationClearsOtherReplicasCache(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)

	// The "other replica": its own pool, its own cache, its own watcher.
	replica := newCapStore(t, rlsPool(t, ctx, f.pool))
	cache, err := limes.NewCachedRevocationChecker(limes.RevocationCacheConfig{Upstream: replica, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	watchRevocations(t, watchCtx, f.pool, cache)

	if r, err := cache.IsRevoked(ctx, f.child); err != nil || r {
		t.Fatalf("child before revoke: revoked=%v err=%v", r, err)
	}

	// Revoked through the first pool — a different connection, as another
	// replica would.
	if err := f.records.Revoke(ctx, limes.RevokeRequest{CapabilityID: f.root, Note: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	deadline := time.Now().Add(notifyDeadline)
	for {
		r, err := cache.IsRevoked(ctx, f.child)
		if err != nil {
			t.Fatalf("IsRevoked: %v", err)
		}
		if r {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child's cached 'live' answer survived its parent's revocation for %s; the notification never cleared the cache", notifyDeadline)
		}
		time.Sleep(notifyPoll)
	}
}
