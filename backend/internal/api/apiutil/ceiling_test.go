package apiutil

import (
	"testing"

	"github.com/oleg-tkachuk/limes"
)

func TestTenantBudgetCeilingOfAPIAmounts(t *testing.T) {
	for _, tc := range []struct {
		n      limes.Nanos
		budget limes.BudgetCeiling
	}{
		{0, limes.BudgetCeiling{Unlimited: true}},
		{1, limes.BudgetCeiling{Max: 1}},
		{limes.MaxNanos, limes.BudgetCeiling{Max: limes.MaxNanos}},
	} {
		if got := BudgetCeilingOf(tc.n); got != tc.budget {
			t.Errorf("BudgetCeilingOf(%d) = %s, want %s", tc.n, got, tc.budget)
		}
		if got := BudgetCeilingAmount(tc.budget); got != tc.n {
			t.Errorf("BudgetCeilingAmount(%s) = %s, want %d", tc.budget, got, tc.n)
		}
	}
}

// A ceiling of nothing has no tenant-budget spelling; it reports as the 0
// that means none rather than as a number a client could send back.
func TestTenantBudgetOfNothingReportsAsZero(t *testing.T) {
	if got := BudgetCeilingAmount(limes.BudgetCeiling{}); got != 0 {
		t.Errorf("BudgetCeilingAmount(0) = %s", got)
	}
}
