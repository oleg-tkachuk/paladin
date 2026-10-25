package capabilityh

import (
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
