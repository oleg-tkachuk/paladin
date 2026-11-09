package app

import (
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	ratelimitpg "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	rcvalkey "github.com/oleg-tkachuk/paladin/backend/internal/ratecount/valkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
)

// buildRateCounter opens the shared store the rate limits count in when the
// configuration names Valkey (ADR-0046), and returns nil for Postgres, whose
// limiters count through the database themselves. stop closes it.
func buildRateCounter(cfg config.Config, log *zap.Logger) (counter ratecount.Counter, stop func()) {
	backend := cfg.Middleware.RateLimit.Backend
	if backend != config.RateLimitBackendValkey {
		log.Info("rate limits count in postgres", zap.String("backend", config.RateLimitBackendPostgres))
		return nil, func() {}
	}
	v := cfg.Datastores.Valkey
	c, stop := rcvalkey.Connect(rcvalkey.Options{
		Addr: v.Addr, Username: v.Username, Password: v.Password, TLS: v.TLS, Timeout: v.Timeout,
	}, log)
	log.Info("rate limits count in valkey", zap.String("backend", backend), zap.String("addr", v.Addr))
	return ratecount.NewDegrading(c, backend, log), stop
}

// tenantRateStore is where the per-tenant limit counts.
func tenantRateStore(deps *SharedDeps) middleware.TenantRateStore {
	if deps.RateCounter != nil {
		return ratecount.NewTenantStore(deps.RateCounter)
	}
	return adapters.NewTenantRateStore(deps.DB.Queries)
}

// tokenRateLimiter is where the per-token limit counts.
func tokenRateLimiter(deps *SharedDeps) (ratelimit.Limiter, error) {
	if deps.RateCounter != nil {
		return ratecount.NewTokenLimiter(deps.RateCounter), nil
	}
	return ratelimitpg.New(deps.Pool)
}
