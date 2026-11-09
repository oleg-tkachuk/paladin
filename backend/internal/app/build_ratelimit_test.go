package app

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount/pgsync"
)

// testValkeyTimeout keeps a dial at an address nothing answers short.
const testValkeyTimeout = 50 * time.Millisecond

// lazyPoolDSN names a server nothing answers at; a pool connects lazily, so
// building one there opens nothing.
const lazyPoolDSN = "postgres://paladin@127.0.0.1:1/paladin"

func TestBuildRateCounterFollowsTheBackend(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), lazyPoolDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	cfg := config.Config{}
	cfg.Middleware.RateLimit.Backend = config.RateLimitBackendPostgres
	cfg.Middleware.RateLimit.SyncInterval = time.Hour
	c, shared, stop := buildRateCounter(cfg, pool, zap.NewNop())
	if _, ok := c.(*pgsync.Counter); !ok {
		t.Fatalf("postgres backend built %T, want one synced through postgres", c)
	}
	if shared != nil {
		t.Fatal("postgres backend reported a valkey store to the health page")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the sync loop hung")
	}

	cfg.Middleware.RateLimit.Backend = config.RateLimitBackendValkey
	cfg.Datastores.Valkey.Addr = "127.0.0.1:1"
	cfg.Datastores.Valkey.Timeout = testValkeyTimeout
	c, shared, stop = buildRateCounter(cfg, pool, zap.NewNop())
	defer stop()
	d, ok := c.(*ratecount.Degrading)
	if !ok {
		t.Fatalf("valkey backend built %T, want one that degrades to the process", c)
	}
	if shared != d {
		t.Fatal("valkey backend did not report the store it counts in to the health page")
	}
}

// The counter is built by the first role that asks for it, once: a process
// that never applies a limit (worker, dispatcher) opens no Valkey client and
// runs no sync loop, and stopping the watchers stops the one that was built.
func TestRateLimitsAreBuiltOnceOnDemand(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), lazyPoolDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := config.Config{}
	cfg.Middleware.RateLimit.Backend = config.RateLimitBackendValkey
	cfg.Datastores.Valkey.Addr = "127.0.0.1:1"
	cfg.Datastores.Valkey.Timeout = testValkeyTimeout
	deps := &SharedDeps{Cfg: cfg, Pool: pool, Logger: zap.NewNop()}

	if n := len(deps.watcherStops); n != 0 {
		t.Fatalf("%d watchers registered before any role asked for the rate limits", n)
	}
	first := deps.RateLimits()
	second := deps.RateLimits()
	if first.Counter == nil || first.Shared == nil || first.Tokens == nil || first.Keys == nil {
		t.Fatalf("rate limits missing a part: %+v", first)
	}
	if first.Counter != second.Counter || first.Shared != second.Shared {
		t.Fatal("a second call built a second counter")
	}
	if n := len(deps.watcherStops); n != 1 {
		t.Fatalf("%d watchers registered, want the counter's one", n)
	}
	done := make(chan struct{})
	go func() { deps.StopWatchers(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the counter hung")
	}
}
