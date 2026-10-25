package cedar

import (
	"slices"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// The operation names are the wire values limes checks caveats against; a
// policy comparing context.capability_op to anything else matches nothing.
func TestCapabilityOps_AreLimesOps(t *testing.T) {
	for got, want := range map[string]limes.Op{
		CapabilityOpGet:    limes.OpGet,
		CapabilityOpList:   limes.OpList,
		CapabilityOpPut:    limes.OpPut,
		CapabilityOpDelete: limes.OpDelete,
		CapabilityOpManage: limes.OpManage,
	} {
		if got != string(want) {
			t.Errorf("%q != limes %q", got, want)
		}
	}
}

// The table the API serves is the policy Cedar evaluates. Under each
// operation, a capability in its own tenant with no tenant policy gains
// exactly the row's actions over a request that admitted nothing — less the
// machine delete permit's, which it holds either way. Grants that never read
// the operation (a member reading its tenant, OAuth consent) cancel out.
func TestCapabilityOperationGrants_MatchTheBuiltin(t *testing.T) {
	for _, action := range machineDeleteActions {
		if authzCapability(t, kindCapability, capOpNone, action, true, "") != DecisionAllow {
			t.Errorf("%s without an operation: denied, but the machine delete permit grants it", action)
		}
	}
	for _, row := range CapabilityOperationGrants {
		for _, action := range declared {
			want := slices.Contains(row.Actions, action) && !slices.Contains(machineDeleteActions, action)
			gained := authzCapability(t, kindCapability, row.Op, action, true, "") == DecisionAllow &&
				authzCapability(t, kindCapability, capOpNone, action, true, "") != DecisionAllow
			if gained != want {
				t.Errorf("%s under %q: gained = %v, the table says %v", action, row.Op, gained, want)
			}
		}
	}
}

// One row per operation limes knows, so the console never meets an operation
// the table is silent about.
func TestCapabilityOperationGrants_CoverEveryOperation(t *testing.T) {
	var ops []string
	for _, row := range CapabilityOperationGrants {
		ops = append(ops, row.Op)
	}
	want := []string{CapabilityOpGet, CapabilityOpList, CapabilityOpPut, CapabilityOpDelete, CapabilityOpManage}
	if !slices.Equal(ops, want) {
		t.Fatalf("operations = %v, want %v", ops, want)
	}
}
