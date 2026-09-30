//go:build integration

package components

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/capability"
)

// SetTenantBudget upserted the cap with no version guard, so two operators
// editing the same tenant's budget raced: the last write won and the loser was
// told it succeeded. Quotas already had this guard (TestQuotaUpsertOCC); the
// budget is the same shape on the same kind of row, and money makes a silent
// overwrite worse, not better.
//
// The check lives in the upsert's DO UPDATE WHERE clause, so this exercises the
// SQL: comparing the version in Go before writing would be a TOCTOU with extra
// steps.
func TestTenantBudgetSetOCC(t *testing.T) {
	ctx, f := newUsageFixture(t)

	t.Run("first write creates at version 0", func(t *testing.T) {
		got, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 100,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if got.ResourceVersion == 0 {
			t.Error("resource_version = 0 after a create; the response carries nothing to send back")
		}
	})

	t.Run("creating twice at version 0 is a conflict", func(t *testing.T) {
		_, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 999,
		})
		if !errors.Is(err, capability.ErrTenantBudgetVersionMismatch) {
			t.Fatalf("err = %v, want ErrTenantBudgetVersionMismatch — 0 asserts the row does not exist", err)
		}
		after, _ := f.usage.GetTenantBudget(ctx, f.tenant)
		if closeEnough(after.MaxBudgetAmount, 999) {
			t.Error("the rejected write landed anyway")
		}
	})

	t.Run("update with the current version succeeds and bumps it", func(t *testing.T) {
		cur, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		got, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 200, ExpectedVersion: cur.ResourceVersion,
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !closeEnough(got.MaxBudgetAmount, 200) {
			t.Errorf("max_budget = %v, want 200", got.MaxBudgetAmount)
		}
		if got.ResourceVersion <= cur.ResourceVersion {
			t.Errorf("resource_version = %d, want > %d — a successful write must advance it, "+
				"or the guard never rejects anything", got.ResourceVersion, cur.ResourceVersion)
		}
	})

	t.Run("stale version is rejected and changes nothing", func(t *testing.T) {
		cur, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_, err = f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID:        f.tenant,
			MaxBudgetAmount: 400,
			ExpectedVersion: cur.ResourceVersion - 1, // what a concurrent writer held
		})
		if !errors.Is(err, capability.ErrTenantBudgetVersionMismatch) {
			t.Fatalf("err = %v, want ErrTenantBudgetVersionMismatch", err)
		}
		after, _ := f.usage.GetTenantBudget(ctx, f.tenant)
		if closeEnough(after.MaxBudgetAmount, 400) {
			t.Error("stale write overwrote the current cap")
		}
		if after.ResourceVersion != cur.ResourceVersion {
			t.Errorf("resource_version moved on a rejected write: %d → %d",
				cur.ResourceVersion, after.ResourceVersion)
		}
	})
}
