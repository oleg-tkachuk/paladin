//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// TestPolicyWatchReconnect proves the LISTEN-reconnect path in
// cedar.PostgresStore.Watch: when the connection holding
// `LISTEN policy_changed` is killed out from under the watcher (Postgres
// restart / failover / network blip, simulated here with
// pg_terminate_backend), the loop re-acquires with backoff, emits a
// ResyncAll control event so the engine flushes its whole cache, and the
// re-established LISTEN keeps delivering targeted invalidations. Without the
// reconnect the goroutine would exit and invalidation would silently degrade
// to the TTL for the rest of the process.
func TestPolicyWatchReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	store := cedar.NewPostgresStore(pool)
	events, err := store.Watch(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	next := func(what string, timeout time.Duration) cedar.ChangeEvent {
		t.Helper()
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("%s: watch channel closed (watcher exited instead of reconnecting)", what)
			}
			return ev
		case <-time.After(timeout):
			t.Fatalf("%s: no ChangeEvent within %s", what, timeout)
		}
		panic("unreachable")
	}

	// Baseline: the live LISTEN delivers a targeted tenant-level event.
	mustExec(t, ctx, pool,
		`UPDATE tenants SET inherited_cedar_policy = 'permit(principal, action, resource);' WHERE id = $1`,
		f.tenantID)
	if ev := next("baseline update", 10*time.Second); ev.ResyncAll || ev.TenantID != f.tenantID || ev.Collection != "" {
		t.Fatalf("baseline event = %+v, want {%s \"\" false}", ev, f.tenantID)
	}

	// Kill the backend the watcher is LISTENing on. WaitForNotification errors,
	// the loop reconnects.
	killListenBackend(t, ctx, pool)

	// The reconnect surfaces as a ResyncAll (terminate itself emits no NOTIFY,
	// so the next event on the channel is the resync).
	if ev := next("post-drop resync", 20*time.Second); !ev.ResyncAll {
		t.Fatalf("post-drop event = %+v, want ResyncAll", ev)
	}
	if n := store.WatchReconnects(); n < 1 {
		t.Fatalf("WatchReconnects = %d, want >= 1", n)
	}

	// The NEW LISTEN is live: a subsequent policy write still invalidates,
	// with the correct targeted payload.
	mustExec(t, ctx, pool,
		`UPDATE collections SET cedar_policy = 'forbid(principal, action, resource);' WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, f.collection)
	if ev := next("post-reconnect update", 10*time.Second); ev.ResyncAll || ev.TenantID != f.tenantID || ev.Collection != f.collection {
		t.Fatalf("post-reconnect event = %+v, want {%s %q false}", ev, f.tenantID, f.collection)
	}
}

// killListenBackend terminates the Postgres backend sitting in
// `LISTEN policy_changed`. It polls first: the watcher issues LISTEN then
// blocks in WaitForNotification, so the backend's last query text settles to
// "LISTEN policy_changed" a beat after Watch returns.
func killListenBackend(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var killed int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM (
				SELECT pg_terminate_backend(pid)
				FROM pg_stat_activity
				WHERE query = 'LISTEN policy_changed' AND pid <> pg_backend_pid()
			) t`).Scan(&killed)
		if err != nil {
			t.Fatalf("terminate LISTEN backend: %v", err)
		}
		if killed > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no LISTEN backend appeared to terminate within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
