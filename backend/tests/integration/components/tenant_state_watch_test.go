//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/tenantstate"
)

// tenantStateNoticeWait is how long a trash, a restore or a purge may take to
// reach another replica's cache. The cache holds answers for an hour here,
// so only the notification can make it change in time.
const tenantStateNoticeWait = 5 * time.Second

// Trashing, restoring and purging a tenant reach a replica's cache by
// notification — as the application role, which is what the gate reads and
// listens as in production.
func TestTenantStateChangesReachTheCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	app := rlsPool(t, ctx, admin)
	tenant, _ := mkTenant(t, ctx, admin, "shared")

	states := middleware.NewCachedTenantStates(tenantstate.NewReader(app), time.Hour)
	watchCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	if err := tenantstate.NewWatcher(app, states.Clear).Start(watchCtx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	await := func(want auth.TenantState) {
		t.Helper()
		deadline := time.Now().Add(tenantStateNoticeWait)
		for {
			got, err := states.TenantState(ctx, tenant)
			if err != nil {
				t.Fatalf("state: %v", err)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("state %v, want %v: the notification never cleared the cache", got, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	await(auth.TenantLive)
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, tenant)
	await(auth.TenantTrashed)
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = NULL WHERE id = $1`, tenant)
	await(auth.TenantLive)
	mustExec(t, ctx, admin, `DELETE FROM tenants WHERE id = $1`, tenant)
	await(auth.TenantMissing)

	if got, err := states.TenantState(ctx, uuid.New()); err != nil || got != auth.TenantMissing {
		t.Errorf("a tenant that never existed: %v, %v", got, err)
	}
}
