//go:build integration

package integration

// A boot that fails after the policy engine started must not leave its LISTEN
// goroutine holding a pooled connection.
//
// The engine's watcher runs on its own background context, so nothing cancels
// it when BuildSharedDeps returns an error — and pgxpool.Close blocks until
// every acquired connection is released. The caller is then stuck in cleanup,
// after the failure, which is the shape app_wiring_test.go records costing an
// afternoon: "a failing request looks like a hung test rather than a failed
// one".
//
// The capability branch defends against this and says so in a comment. The
// api_token branch immediately below it did not, so a bad api_token key hung
// whatever tried to close the pool afterwards — including every helper in
// this package that registers t.Cleanup(db.Close) before calling
// BuildSharedDeps.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

func TestBuildSharedDeps_FailedBootLeavesThePoolClosable(t *testing.T) {
	ctx := context.Background()
	h := pgharness.Setup(t)

	cfg := wiringConfig(t, h.MigrateDSN)
	// Fails inside BuildAPITokenBundle: the hasher rejects a short HMAC key.
	// Everything before it — registry warmup, repos, the policy engine and
	// its watcher — succeeds, which is the only interesting case.
	cfg.APIToken.Enabled = true
	cfg.APIToken.HMACKey = "too-short"

	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop())
	if err == nil {
		t.Fatalf("BuildSharedDeps accepted a %d-byte HMAC key", len(cfg.APIToken.HMACKey))
	}
	if deps != nil {
		// Nothing can stop the watchers through a nil return, so the failing
		// branch has to have done it already.
		t.Fatalf("BuildSharedDeps returned deps alongside the error: %+v", deps)
	}

	// The assertion: closing the pool now must not block. Without the cleanup
	// this never returns and the test dies on the package timeout — which is
	// precisely how the failure presents in real life.
	done := make(chan struct{})
	go func() {
		db.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("db.Close blocked after a failed boot — a LISTEN watcher still holds a pooled connection")
	}
}
