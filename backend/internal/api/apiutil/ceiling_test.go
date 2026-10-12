package apiutil

import (
	"testing"

	"github.com/oleg-tkachuk/limes"
)

func TestCeilingsOfAPINumbers(t *testing.T) {
	for _, tc := range []struct {
		n        int64
		requests limes.RequestCeiling
		budget   limes.BudgetCeiling
	}{
		{0, limes.RequestCeiling{Unlimited: true}, limes.BudgetCeiling{Unlimited: true}},
		{1, limes.RequestCeiling{Max: 1}, limes.BudgetCeiling{Max: 1}},
		{int64(limes.MaxNanos), limes.RequestCeiling{Max: int64(limes.MaxNanos)}, limes.BudgetCeiling{Max: limes.MaxNanos}},
	} {
		if got := RequestCeilingOf(tc.n); got != tc.requests {
			t.Errorf("RequestCeilingOf(%d) = %s, want %s", tc.n, got, tc.requests)
		}
		if got := BudgetCeilingOf(limes.Nanos(tc.n)); got != tc.budget {
			t.Errorf("BudgetCeilingOf(%d) = %s, want %s", tc.n, got, tc.budget)
		}
		if got := RequestCeilingNumber(tc.requests); got != tc.n {
			t.Errorf("RequestCeilingNumber(%s) = %d, want %d", tc.requests, got, tc.n)
		}
		if got := BudgetCeilingAmount(tc.budget); got != limes.Nanos(tc.n) {
			t.Errorf("BudgetCeilingAmount(%s) = %s, want %d", tc.budget, got, tc.n)
		}
	}
}

// A ceiling of nothing has no API spelling; it reports as the 0 that means
// none rather than as a number a client could send back.
func TestCeilingOfNothingReportsAsZero(t *testing.T) {
	if got := RequestCeilingNumber(limes.RequestCeiling{}); got != 0 {
		t.Errorf("RequestCeilingNumber(0) = %d", got)
	}
	if got := BudgetCeilingAmount(limes.BudgetCeiling{}); got != 0 {
		t.Errorf("BudgetCeilingAmount(0) = %s", got)
	}
}

// A delegation's 0 keeps the parent's ceiling, never Unlimited, which would
// be refused as wider than any bounded parent.
func TestDelegatedCeilingsKeepTheParentsForZero(t *testing.T) {
	bounded := limes.RequestCeiling{Max: 7}
	if got := DelegatedRequests(0, bounded); got != bounded {
		t.Errorf("DelegatedRequests(0, %s) = %s", bounded, got)
	}
	if got := DelegatedRequests(3, bounded); got != (limes.RequestCeiling{Max: 3}) {
		t.Errorf("DelegatedRequests(3, %s) = %s", bounded, got)
	}
	budget := limes.BudgetCeiling{Unlimited: true}
	if got := DelegatedBudget(0, budget); got != budget {
		t.Errorf("DelegatedBudget(0, %s) = %s", budget, got)
	}
	if got := DelegatedBudget(5, budget); got != (limes.BudgetCeiling{Max: 5}) {
		t.Errorf("DelegatedBudget(5, %s) = %s", budget, got)
	}
}
