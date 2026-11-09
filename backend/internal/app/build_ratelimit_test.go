package app

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go.uber.org/zap"

	ratelimitpg "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
)

// testValkeyTimeout keeps a dial at an address nothing answers short.
const testValkeyTimeout = 50 * time.Millisecond

// lazyPoolDSN names a server nothing answers at; a pool connects lazily, so
// building one there opens nothing.
const lazyPoolDSN = "postgres://paladin@127.0.0.1:1/paladin"

func TestBuildRateCounterFollowsTheBackend(t *testing.T) {
	cfg := config.Config{}
	cfg.Middleware.RateLimit.Backend = config.RateLimitBackendPostgres
	c, stop := buildRateCounter(cfg, zap.NewNop())
	stop()
	if c != nil {
		t.Fatalf("postgres backend built a shared counter %T", c)
	}

	cfg.Middleware.RateLimit.Backend = config.RateLimitBackendValkey
	cfg.Datastores.Valkey.Addr = "127.0.0.1:1"
	cfg.Datastores.Valkey.Timeout = testValkeyTimeout
	c, stop = buildRateCounter(cfg, zap.NewNop())
	defer stop()
	if _, ok := c.(*ratecount.Degrading); !ok {
		t.Fatalf("valkey backend built %T, want one that degrades to the process", c)
	}
}

func TestLimitersCountWhereTheDepsSay(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), lazyPoolDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	pg := &SharedDeps{DB: &postgres.DB{}, Pool: pool}
	if _, ok := tenantRateStore(pg).(*adapters.TenantRateStore); !ok {
		t.Fatalf("tenant limit without a counter: %T", tenantRateStore(pg))
	}
	pl, err := tokenRateLimiter(pg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pl.(*ratelimitpg.Limiter); !ok {
		t.Fatalf("token limit without a counter: %T", pl)
	}

	shared := &SharedDeps{RateCounter: ratecount.NewLocal()}
	if _, ok := tenantRateStore(shared).(*ratecount.TenantStore); !ok {
		t.Fatalf("tenant limit with a counter: %T", tenantRateStore(shared))
	}
	l, err := tokenRateLimiter(shared)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.(*ratecount.TokenLimiter); !ok {
		t.Fatalf("token limit with a counter: %T", l)
	}
}
