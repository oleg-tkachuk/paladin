package cedar

import (
	"slices"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The operation names are the wire values caveats are checked against: a
// policy comparing context.capability_op to anything else matches nothing.
func TestCapabilityOps_AreTheWireOps(t *testing.T) {
	for got, want := range map[string]string{
		CapabilityOpGet:     string(limes.OpGet),
		CapabilityOpList:    string(limes.OpList),
		CapabilityOpPut:     string(limes.OpPut),
		CapabilityOpDelete:  string(limes.OpDelete),
		CapabilityOpShare:   string(limes.OpShare),
		CapabilityOpManage:  string(limes.OpManage),
		CapabilityOpPresign: string(paladin.OpObjectPresign),
		CapabilityOpTag:     string(paladin.OpObjectTag),
	} {
		if got != want {
			t.Errorf("%q != wire %q", got, want)
		}
	}
}

// The RPC the table says share opens is the one the SDK calls.
func TestCapabilityOperationGrants_NameTheDelegateProcedure(t *testing.T) {
	want := strings.ReplaceAll(strings.TrimPrefix(paladinadminv1connect.CapabilityServiceDelegateProcedure, "/"), "/", ".")
	if procedureDelegate != want {
		t.Fatalf("procedureDelegate = %q, want %q", procedureDelegate, want)
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

// Each row carries what its basis says and nothing else, so the console can
// read the basis without second-guessing the lists.
func TestCapabilityOperationGrants_RowsMatchTheirBasis(t *testing.T) {
	ops := map[string]bool{}
	for _, row := range CapabilityOperationGrants {
		ops[row.Op] = true
	}
	for _, row := range CapabilityOperationGrants {
		if got, want := len(row.Actions) > 0, row.Basis == GrantBuiltin; got != want {
			t.Errorf("%s: has actions = %v, basis %d", row.Op, got, row.Basis)
		}
		if got, want := len(row.PairedOps) > 0, row.Basis == GrantPaired; got != want {
			t.Errorf("%s: has paired ops = %v, basis %d", row.Op, got, row.Basis)
		}
		if got, want := len(row.Procedures) > 0, row.Basis == GrantCaveatsOnly; got != want {
			t.Errorf("%s: has procedures = %v, basis %d", row.Op, got, row.Basis)
		}
		for _, p := range row.PairedOps {
			if !ops[p] {
				t.Errorf("%s pairs with %q, which has no row", row.Op, p)
			}
		}
	}
}

// One row per operation a caveat can name, so the console never meets an
// operation the table is silent about.
func TestCapabilityOperationGrants_CoverEveryOperation(t *testing.T) {
	var ops []string
	for _, row := range CapabilityOperationGrants {
		ops = append(ops, row.Op)
	}
	want := []string{
		CapabilityOpGet, CapabilityOpPut, CapabilityOpList, CapabilityOpDelete,
		CapabilityOpShare, CapabilityOpManage, CapabilityOpPresign, CapabilityOpTag,
	}
	if !slices.Equal(ops, want) {
		t.Fatalf("operations = %v, want %v", ops, want)
	}
}
