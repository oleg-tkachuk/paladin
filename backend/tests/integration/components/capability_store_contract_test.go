//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"

	"github.com/oleg-tkachuk/limes/storetest"
)

// The module's Store contract against the relational store.
func TestStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Env {
		t.Helper()
		ctx := context.Background()
		pool := startPostgres(t)
		tenant, _ := mkTenant(t, ctx, pool, "shared")
		return storetest.Env{Ctx: ctx, Store: newCapStore(t, pool), Tenant: tenant}
	})
}

// The same contract on the app role, without BYPASSRLS, scoped to the tenant
// as a request is: GetRecord's revocation join and every revocation read
// work through the policies the server runs under.
func TestStoreContractUnderRowLevelSecurity(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Env {
		t.Helper()
		ctx := context.Background()
		admin := startPostgres(t)
		tenant, _ := mkTenant(t, ctx, admin, "shared")
		pool := rlsPool(t, ctx, admin)
		return storetest.Env{Ctx: auth.WithActingTenant(ctx, tenant), Store: newCapStore(t, pool), Tenant: tenant}
	})
}
