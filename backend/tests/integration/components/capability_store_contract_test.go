//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/capability/storetest"
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
