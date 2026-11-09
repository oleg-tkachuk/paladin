package app

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount/pgsync"
	rcvalkey "github.com/oleg-tkachuk/paladin/backend/internal/ratecount/valkey"
)

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
