package capabilityh

import (
	"slices"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The response is the engine's table, row for row: the console shows what
// the built-in policy grants, not a copy of it.
func TestListOperationGrants_ServesTheEngineTable(t *testing.T) {
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
	resp, err := h.ListOperationGrants(adminCtx(), &adminv1.CapabilityServiceListOperationGrantsRequest{})
	if err != nil {
		t.Fatalf("ListOperationGrants: %v", err)
	}
	got := resp.GetGrants()
	if len(got) != len(cedar.CapabilityOperationGrants) {
		t.Fatalf("%d grants, want %d", len(got), len(cedar.CapabilityOperationGrants))
	}
	for i, want := range cedar.CapabilityOperationGrants {
		if got[i].GetOp() != want.Op {
			t.Errorf("grant %d op = %q, want %q", i, got[i].GetOp(), want.Op)
		}
		if got[i].GetBasis() != grantBasisToProto[want.Basis] || got[i].GetBasis() == adminv1.CapabilityGrantBasis_CAPABILITY_GRANT_BASIS_UNSPECIFIED {
			t.Errorf("%s basis = %v, want %v", want.Op, got[i].GetBasis(), grantBasisToProto[want.Basis])
		}
		if !slices.Equal(got[i].GetPairedOps(), want.PairedOps) || !slices.Equal(got[i].GetProcedures(), want.Procedures) {
			t.Errorf("%s paired %v procedures %v, want %v %v", want.Op, got[i].GetPairedOps(), got[i].GetProcedures(), want.PairedOps, want.Procedures)
		}
		if len(got[i].GetActions()) != len(want.Actions) {
			t.Errorf("%s: %d actions, want %d", want.Op, len(got[i].GetActions()), len(want.Actions))
			continue
		}
		for j, a := range want.Actions {
			if got[i].GetActions()[j] != a.String() {
				t.Errorf("%s action %d = %q, want %q", want.Op, j, got[i].GetActions()[j], a)
			}
		}
	}
}

func TestListOperationGrants_NeedsReadCapability(t *testing.T) {
	h := mkHandler(t, &fakeStore{}, nil, &denyAuthorizer{})
	_, err := h.ListOperationGrants(adminCtx(), &adminv1.CapabilityServiceListOperationGrantsRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}
