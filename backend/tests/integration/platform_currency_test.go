//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The platform's currency (capability.currency) stands in for limes'
// DefaultUnitCode — USD — wherever nothing names a unit. Here it is EUR, so a
// leak of the old default shows as USD.
const platformCurrency = "EUR"

func TestPlatformCurrency_IsWhatAnUnnamedUnitMeans(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	store := newUsageStore(t, h.PoolMigrate, capabilitypg.WithDefaultUnit(platformCurrency))
	handler := billingh.NewHandler(h.PoolMigrate, store, allowAuth{}).WithCurrency(platformCurrency)

	t.Run("an empty summary", func(t *testing.T) {
		tenant := mustCreateTenant(t, h.PoolMigrate, "cur-empty")
		end := time.Now().UTC()
		s, err := handler.GetTenantSummary(ctxAdmin(t, tenant), tenant, end.Add(-time.Hour), end)
		if err != nil {
			t.Fatalf("GetTenantSummary: %v", err)
		}
		if s.UnitCode != platformCurrency {
			t.Errorf("unit = %q, want the platform's %q", s.UnitCode, platformCurrency)
		}
	})

	t.Run("a first tenant budget that names no unit", func(t *testing.T) {
		tenant := mustCreateTenant(t, h.PoolMigrate, "cur-budget")
		b, err := store.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
			TenantID: tenant, Ceiling: limes.BudgetCeiling{Unlimited: true},
		})
		if err != nil {
			t.Fatalf("SetTenantBudget: %v", err)
		}
		if b.UnitCode != platformCurrency {
			t.Errorf("unit = %q, want the platform's %q", b.UnitCode, platformCurrency)
		}
		// A later set that names none keeps the unit the budget has.
		b, err = store.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
			TenantID: tenant, Ceiling: limes.BudgetCeiling{Unlimited: true}, ExpectedVersion: b.ResourceVersion,
		})
		if err != nil || b.UnitCode != platformCurrency {
			t.Errorf("second set: unit = %q, %v; want it kept", b.UnitCode, err)
		}
	})
}
