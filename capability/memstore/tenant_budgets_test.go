package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

// seedBudget sets a tenant's ceiling and charges spent against it.
func seedBudget(t *testing.T, u *UsageStore[struct{}], ceiling, spent float64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := u.SetTenantBudget(ctx, capability.SetTenantBudgetRequest{TenantID: tenant, MaxBudgetAmount: ceiling}); err != nil {
		t.Fatal(err)
	}
	if spent > 0 {
		if _, err := u.Charge(ctx, capability.ChargeRequest{
			CapabilityID: uuid.New(), TenantID: tenant, Amount: spent, Overrun: capability.OverrunRecord,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return tenant
}

func TestListTenantBudgets(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	over := seedBudget(t, u, 10, 15) // past its ceiling: clamped to 100%
	half := seedBudget(t, u, 10, 5)  // 50%
	low := seedBudget(t, u, 10, 1)   // 10%
	unlimited := seedBudget(t, u, 0, 3)

	cases := []struct {
		name string
		req  capability.ListTenantBudgetsRequest
		want []uuid.UUID
	}{
		{"most at risk first", capability.ListTenantBudgetsRequest{}, []uuid.UUID{over, half, low, unlimited}},
		{"threshold", capability.ListTenantBudgetsRequest{ThresholdPct: 50}, []uuid.UUID{over, half}},
		{"unlimited only", capability.ListTenantBudgetsRequest{UnlimitedOnly: true}, []uuid.UUID{unlimited}},
		{"limit", capability.ListTenantBudgetsRequest{Limit: 2}, []uuid.UUID{over, half}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := u.ListTenantBudgets(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			var ids []uuid.UUID
			for _, s := range got {
				ids = append(ids, s.TenantID)
			}
			if len(ids) != len(tc.want) {
				t.Fatalf("listed %v, want %v", ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("listed %v, want %v", ids, tc.want)
				}
			}
		})
	}
	got, _ := u.ListTenantBudgets(ctx, capability.ListTenantBudgetsRequest{Limit: 1})
	if got[0].UtilisationPct != maxUtilisationPct {
		t.Errorf("a tenant past its ceiling reads %v%%, want %d%%", got[0].UtilisationPct, maxUtilisationPct)
	}
}

// ResetSpend closes the period: the spend goes to zero and the period starts
// again; without it the ceiling changes and the spend stands.
func TestSetTenantBudgetResetSpend(t *testing.T) {
	ctx := context.Background()
	u := NewUsage[struct{}](nil)
	tenant := seedBudget(t, u, 10, 4)
	b, _ := u.GetTenantBudget(ctx, tenant)

	b, err := u.SetTenantBudget(ctx, capability.SetTenantBudgetRequest{
		TenantID: tenant, MaxBudgetAmount: 20, ExpectedVersion: b.ResourceVersion,
	})
	if err != nil || b.SpentAmount != 4 || b.MaxBudgetAmount != 20 {
		t.Fatalf("raising the ceiling = %+v, %v; want 20 with 4 still spent", b, err)
	}
	start := b.PeriodStart
	b, err = u.SetTenantBudget(ctx, capability.SetTenantBudgetRequest{
		TenantID: tenant, MaxBudgetAmount: 20, ResetSpend: true, ExpectedVersion: b.ResourceVersion,
	})
	if err != nil || b.SpentAmount != 0 || b.PeriodStart.Before(start) {
		t.Fatalf("closing the period = %+v, %v; want nothing spent and a new period", b, err)
	}
	if _, err := u.SetTenantBudget(ctx, capability.SetTenantBudgetRequest{
		TenantID: tenant, MaxBudgetAmount: 30, ExpectedVersion: b.ResourceVersion - 1,
	}); !errors.Is(err, capability.ErrTenantBudgetVersionMismatch) {
		t.Errorf("a stale version: err = %v, want ErrTenantBudgetVersionMismatch", err)
	}
	if _, err := u.GetTenantBudget(ctx, uuid.New()); !errors.Is(err, capability.ErrTenantBudgetNotFound) {
		t.Errorf("an unknown tenant: err = %v, want ErrTenantBudgetNotFound", err)
	}
}
