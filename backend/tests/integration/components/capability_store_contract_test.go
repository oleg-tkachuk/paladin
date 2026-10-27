//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes/storetest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// storeContractEnv is the module's Store contract over pool, on a clock the
// suite moves. scoped runs each tenant's calls scoped to it, as a request is.
func storeContractEnv(t *testing.T, admin, pool *pgxpool.Pool, scoped bool) storetest.Env {
	t.Helper()
	ctx := context.Background()
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	clock := newFakeClock()
	store, err := capstore.New(pool, capstore.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	tenantCtx := func(id uuid.UUID) context.Context {
		if scoped {
			return auth.WithActingTenant(ctx, id)
		}
		return ctx
	}
	return storetest.Env{
		Ctx:    tenantCtx(tenant),
		Store:  store,
		Tenant: tenant,
		NewTenant: func() (uuid.UUID, error) {
			id, _ := mkTenant(t, ctx, admin, "shared")
			return id, nil
		},
		TenantCtx: tenantCtx,
		// Scoped, a call under another tenant's context is refused.
		ScopesTenants: scoped,
		Now:           clock.Now,
		Advance:       clock.Advance,
		DeleteTenant: func(id uuid.UUID) error {
			_, err := admin.Exec(ctx, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, id)
			return err
		},
	}
}

// The module's Store contract against the relational store.
func TestStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Env {
		t.Helper()
		admin := startPostgres(t)
		return storeContractEnv(t, admin, admin, false)
	})
}

// The same contract on the app role, without BYPASSRLS, scoped to the tenant
// as a request is: GetRecord's revocation join and every revocation read
// work through the policies the server runs under.
func TestStoreContractUnderRowLevelSecurity(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Env {
		t.Helper()
		admin := startPostgres(t)
		return storeContractEnv(t, admin, rlsPool(t, context.Background(), admin), true)
	})
}
