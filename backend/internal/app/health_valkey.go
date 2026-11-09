package app

import (
	"context"
	"strconv"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
)

// valkeyComponentName is the Valkey row on the health page.
const valkeyComponentName = "valkey"

// Detail names and values the valkey component reports; the console shows
// them as they are.
const (
	detailValkeyAddr     = "address"
	detailLimitsCounted  = "rate limits counted"
	detailDecidedLocally = "decided per process"

	limitsCountedShared     = "shared"
	limitsCountedPerProcess = "per process"
)

// valkeyComponent reports the Valkey the rate limits count in (ADR-0046).
// shared is nil when they count in postgres, and the row is then off by
// middleware.rate_limit.backend. On, it pings Valkey; never critical, since a
// Valkey that does not answer leaves each limit decided per process, at the
// full rate on every replica, rather than refusing requests.
func valkeyComponent(addr string, shared *ratecount.Degrading) health.Check {
	return health.Check{
		Name:     valkeyComponentName,
		Category: health.CategoryDatabase,
		Switch:   health.Fixed(health.ByConfig(shared != nil, config.KeyRateLimitBackend)),
		Func:     func(ctx context.Context) error { return shared.Ping(ctx) },
		Describe: func(context.Context) []health.Detail { return valkeyDetails(addr, shared.Stats()) },
	}
}

func valkeyDetails(addr string, st ratecount.Stats) []health.Detail {
	counted := limitsCountedShared
	if st.PerProcess {
		counted = limitsCountedPerProcess
	}
	return []health.Detail{
		{Name: detailValkeyAddr, Value: addr},
		// As of the last limit decision, which a ping does not change.
		{Name: detailLimitsCounted, Value: counted},
		// Since the pod started.
		{Name: detailDecidedLocally, Value: strconv.FormatUint(st.DecidedPerProcess, 10)},
	}
}
