package app

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/tenantstate"
)

// buildTenantGate is the interceptor that refuses a trashed or missing
// tenant's credentials, over a cache of tenant states the tenant_state
// watcher clears on every change. The watcher holds a pooled connection for
// the process lifetime, so it runs on its own context, which StopWatchers
// cancels before the pool is closed — as the revocation watcher does.
func buildTenantGate(deps *SharedDeps) (connect.Interceptor, error) {
	states := middleware.NewCachedTenantStates(tenantstate.NewReader(deps.Pool), middleware.DefaultTenantStateTTL)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	if err := tenantstate.NewWatcher(deps.Pool, states.Clear).Start(watchCtx); err != nil {
		stopWatch()
		return nil, fmt.Errorf("app: tenant state watch: %w", err)
	}
	deps.RegisterWatcherStop(stopWatch)
	return middleware.TenantGate(states), nil
}
