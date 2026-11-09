package app

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount/pgsync"
	rcvalkey "github.com/oleg-tkachuk/paladin/backend/internal/ratecount/valkey"
)

// RateLimits is where the rate limits a plane applies count (ADR-0046).
type RateLimits struct {
	// Counter is where the tenant and token limits count.
	Counter ratecount.Counter
	// Shared is Counter when the limits count in Valkey, for the health
	// page; nil when they count in postgres.
	Shared *ratecount.Degrading
	// Tokens enforces a token's per-minute cap on Counter. A token with no
	// cap never reaches it, and one capped at 0 is refused before it.
	Tokens ratelimit.Limiter
	// Keys holds the login and OAuth token limits on Counter, each keyed by
	// what the caller sends.
	Keys *ratecount.KeyLimiter
}

// RateLimits builds where the rate limits count, on the first call, for the
// roles that apply them: the api and admin planes. A role that never asks —
// worker, dispatcher, ingest — opens no Valkey client and runs no sync loop,
// and its NetworkPolicy does not let it out to Valkey. The counter stops with
// the other watchers, before the pool closes, so its last sync still has a
// connection.
func (d *SharedDeps) RateLimits() RateLimits {
	d.rateOnce.Do(func() {
		counter, shared, stop := buildRateCounter(d.Cfg, d.Pool, d.Logger)
		d.RegisterWatcherStop(stop)
		d.rateLimits = RateLimits{
			Counter: counter, Shared: shared,
			Tokens: ratecount.NewTokenLimiter(counter), Keys: ratecount.NewKeyLimiter(counter),
		}
	})
	return d.rateLimits
}

// buildRateCounter builds where the tenant and token rate limits count
// (ADR-0046): Valkey when the configuration names it, otherwise this process
// synced through Postgres via db. shared is the Valkey store the health page
// reports on, nil for postgres. stop ends the background work and closes what
// it opened.
func buildRateCounter(cfg config.Config, db pgsync.Batcher, log *zap.Logger) (counter ratecount.Counter, shared *ratecount.Degrading, stop func()) {
	rl := cfg.Middleware.RateLimit
	if rl.Backend == config.RateLimitBackendValkey {
		v := cfg.Datastores.Valkey
		c, stop := rcvalkey.Connect(rcvalkey.Options{
			Addr: v.Addr, Username: v.Username, Password: v.Password, TLS: v.TLS, Timeout: v.Timeout,
		}, log)
		log.Info("rate limits count in valkey", zap.String("addr", v.Addr))
		d := ratecount.NewDegrading(c, rl.Backend, log)
		return d, d, stop
	}
	c := pgsync.New(pgsync.NewPostgresStore(db), rl.SyncInterval, log)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.Run(ctx) }()
	log.Info("rate limits count in this process, synced through postgres",
		zap.Duration("sync_interval", rl.SyncInterval))
	return c, nil, func() { cancel(); wg.Wait() }
}
