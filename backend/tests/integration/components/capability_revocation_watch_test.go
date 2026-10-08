//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

// A revocation committed anywhere must reach a replica's cache by
// notification, not by waiting out the TTL. The cache here holds answers for
// an hour, so only the watcher can make the revoked capability — and its
// child, cached under its own id — fail promptly.
func TestRevocationNotificationClearsOtherReplicasCache(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)

	// The "other replica": its own pool, its own cache, its own watcher.
	replica := newCapStore(t, rlsPool(t, ctx, f.pool))
	cache := capability.NewCachedRevocationChecker(replica, time.Hour)
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	watcher := capstore.NewRevocationWatcher(f.pool, cache.Clear)
	if err := watcher.Start(watchCtx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	if r, err := cache.IsRevoked(ctx, f.child); err != nil || r {
		t.Fatalf("child before revoke: revoked=%v err=%v", r, err)
	}

	// Revoked through the first pool — a different connection, as another
	// replica would.
	if err := f.records.Revoke(ctx, capability.RevokeRequest{ID: f.root, Reason: "test", Actor: "user:ops"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := cache.IsRevoked(ctx, f.child)
		if err != nil {
			t.Fatalf("IsRevoked: %v", err)
		}
		if r {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the child's cached 'live' answer survived its parent's revocation for 5s; the notification never cleared the cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
