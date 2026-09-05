package platformstats

import "testing"

// The first tests in this package. Everything else here takes a
// *pgxpool.Pool and is held — where it is held at all — by
// internal/integration; these two functions are pure, and they encode
// promises the surrounding comments make to the console.

// stateOrder pins the lifecycle sequence so the console renders the same
// columns every poll, with unknown states after the known ones,
// alphabetically.
func TestSortStates_PinsTheLifecycleOrder(t *testing.T) {
	in := []StateStat{
		{State: "ZETA"}, {State: "DELETED"}, {State: "PENDING"},
		{State: "ALPHA"}, {State: "AVAILABLE"}, {State: "FAILED"},
	}
	sortStates(in)

	want := []string{"PENDING", "AVAILABLE", "FAILED", "DELETED", "ALPHA", "ZETA"}
	for i, w := range want {
		if in[i].State != w {
			got := make([]string, len(in))
			for j, s := range in {
				got[j] = s.State
			}
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// sortTenants ranks by object count descending and breaks ties on tenant_id
// "so the order is stable across polls". Relaxing the count comparison to
// >= makes less(i,j) and less(j,i) both true for equal counts, which is not
// a strict weak ordering: the tie-break never runs and the console reshuffles
// equal-sized tenants between polls for no reason an operator can see.
func TestSortTenants_BreaksTiesOnTenantIDForAStableOrder(t *testing.T) {
	in := []TenantStat{
		{TenantID: "bbb", TotalCount: 5},
		{TenantID: "aaa", TotalCount: 5},
		{TenantID: "ccc", TotalCount: 9},
	}
	sortTenants(in)

	want := []string{"ccc", "aaa", "bbb"}
	for i, w := range want {
		if in[i].TenantID != w {
			got := make([]string, len(in))
			for j, t2 := range in {
				got[j] = t2.TenantID
			}
			t.Fatalf("order %v, want %v — equal counts must fall back to "+
				"tenant_id, or the ranking changes between identical polls", got, want)
		}
	}
}
