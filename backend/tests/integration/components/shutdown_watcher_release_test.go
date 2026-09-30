//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auditstream"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// TestShutdownReleasesWatcherConns reproduces the fx-migration shutdown deadlock
// (PRs #152 / #153) at the connection-holder level.
//
// Two process-lifetime goroutines each hold a pooled Postgres connection via
// LISTEN: the Cedar policy engine's watcher (started by BuildSharedDeps) and the
// admin audit-stream hub (started by AssembleAdminMux). Each is released only
// when its context is cancelled. Under fx those contexts were context.Background()
// — never cancelled — so a shutdown that closed the pool first deadlocked
// pgxpool.Close() until the pod hit its termination grace and was SIGKILLed.
//
// The fix routes every such watcher's cancel through SharedDeps.StopWatchers(),
// which every shutdown path runs BEFORE db.Close(). This test wires both real
// watchers the way the app does, registers them, and asserts StopWatchers()
// releases their connections so Close() completes instead of hanging. Without
// the release-on-stop guarantee, the AcquiredConns / Close assertions below
// time out — exactly the production failure.
func TestShutdownReleasesWatcherConns(t *testing.T) {
	pool := startPostgres(t)
	log := zap.NewNop()

	// SharedDeps is the umbrella: every connection-holding watcher registers its
	// cancel here, and StopWatchers() fans out to all of them.
	var deps app.SharedDeps

	// (1) Cedar policy engine LISTEN watcher — mirrors BuildSharedDeps.
	cedarCtx, cedarCancel := context.WithCancel(context.Background())
	store := cedar.NewPostgresStore(pool)
	if _, err := store.Watch(cedarCtx); err != nil {
		cedarCancel()
		t.Fatalf("cedar watch: %v", err)
	}
	deps.RegisterWatcherStop(cedarCancel)

	// (2) Admin audit-stream LISTEN hub — mirrors AssembleAdminMux.
	hubCtx, hubCancel := context.WithCancel(context.Background())
	hub := auditstream.NewHub(log.Named("audit-stream"))
	go hub.Run(hubCtx, pool)
	deps.RegisterWatcherStop(hubCancel)

	// Both watchers each hold exactly one pooled LISTEN connection; wait until
	// they are actually acquired so the release assertion below is meaningful.
	if !waitForCond(5*time.Second, func() bool { return pool.Stat().AcquiredConns() >= 2 }) {
		t.Fatalf("watchers never acquired their LISTEN connections (acquired=%d, want >=2)",
			pool.Stat().AcquiredConns())
	}

	// The single umbrella call every shutdown path runs before db.Close().
	deps.StopWatchers()

	// The held connections MUST be released promptly — otherwise pgxpool.Close()
	// (and, in production, db.Close()) deadlocks until SIGKILL.
	if !waitForCond(10*time.Second, func() bool { return pool.Stat().AcquiredConns() == 0 }) {
		t.Fatalf("StopWatchers() did not release the LISTEN connections (still acquired=%d) — the fx-shutdown SIGKILL bug",
			pool.Stat().AcquiredConns())
	}

	// Direct reproduction of the production failure: Close must not block now
	// that the watchers are stopped. (pgxpool.Close is idempotent, so the
	// harness's t.Cleanup(pool.Close) second call is a no-op.)
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pool.Close() deadlocked after StopWatchers() — a watcher is still holding a connection")
	}
}

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}
